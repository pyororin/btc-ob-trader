package datastore

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// MarketEvent is an interface for market events (trades, order book updates).
type MarketEvent interface {
	GetTime() time.Time
}

// TradeEvent represents a single trade event from the database.
type TradeEvent struct {
	Trade coincheck.TradeData
	Time  time.Time
}

func (e TradeEvent) GetTime() time.Time { return e.Time }

// OrderBookEvent represents an order book snapshot/update from the database.
type OrderBookEvent struct {
	OrderBook coincheck.OrderBookData
	Time      time.Time
}

func (e OrderBookEvent) GetTime() time.Time { return e.Time }

// Repository handles database operations for fetching backtest data.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// FetchMarketEvents fetches trades and order book updates from the database
// within the given time range and returns them as a sorted slice of MarketEvent.
func (r *Repository) FetchMarketEvents(ctx context.Context, pair string, startTime, endTime time.Time) ([]MarketEvent, error) {
	trades, err := r.fetchTrades(ctx, pair, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch trades: %w", err)
	}

	orderBooks, err := r.fetchOrderBookUpdates(ctx, pair, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch order book updates: %w", err)
	}

	// Combine and sort events
	events := make([]MarketEvent, 0, len(trades)+len(orderBooks))
	for _, t := range trades {
		events = append(events, t)
	}
	for _, ob := range orderBooks {
		events = append(events, ob)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].GetTime().Before(events[j].GetTime())
	})

	return events, nil
}

func (r *Repository) fetchTrades(ctx context.Context, pair string, startTime, endTime time.Time) ([]TradeEvent, error) {
	query := `
        SELECT transaction_id, pair, price, size, side, time
        FROM trades
        WHERE pair = $1 AND time >= $2 AND time < $3
        ORDER BY time ASC;
    `
	rows, err := r.db.Query(ctx, query, pair, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []TradeEvent
	for rows.Next() {
		var t struct {
			TransactionID int64
			Pair          string
			Price         float64
			Size          float64
			Side          string
			Time          time.Time
		}
		if err := rows.Scan(&t.TransactionID, &t.Pair, &t.Price, &t.Size, &t.Side, &t.Time); err != nil {
			return nil, err
		}
		trades = append(trades, TradeEvent{
			Trade: coincheck.TradeData{
				ID:        fmt.Sprintf("%d", t.TransactionID),
				PairStr:   t.Pair,
				RateStr:   fmt.Sprintf("%f", t.Price),
				AmountStr: fmt.Sprintf("%f", t.Size),
				SideStr:   t.Side,
			},
			Time: t.Time,
		})
	}
	return trades, rows.Err()
}

func (r *Repository) fetchOrderBookUpdates(ctx context.Context, pair string, startTime, endTime time.Time) ([]OrderBookEvent, error) {
	// This is a simplified implementation. A real implementation would need to
	// reconstruct the order book state at each point in time.
	// For now, we fetch snapshots and treat them as individual events.
	query := `
        SELECT time, side, price, size
        FROM order_book_updates
        WHERE pair = $1 AND time >= $2 AND time < $3 AND is_snapshot = TRUE
        ORDER BY time ASC;
    `
	// This query is likely insufficient for a proper backtest.
	// A full backtest would require reconstructing the book from a snapshot and subsequent diffs.
	// For this iteration, we are assuming simplified logic where each "update" can be treated as a state.
	rows, err := r.db.Query(ctx, query, pair, startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Group updates by timestamp
	updatesByTime := make(map[time.Time][][]string)
	for rows.Next() {
		var t time.Time
		var side string
		var price, size float64
		if err := rows.Scan(&t, &side, &price, &size); err != nil {
			return nil, err
		}
		rateStr := fmt.Sprintf("%f", price)
		amountStr := fmt.Sprintf("%f", size)
		updatesByTime[t] = append(updatesByTime[t], []string{rateStr, amountStr, side})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var events []OrderBookEvent
	for t, updates := range updatesByTime {
		var bids, asks [][]string
		for _, u := range updates {
			if u[2] == "bid" {
				bids = append(bids, u[:2])
			} else {
				asks = append(asks, u[:2])
			}
		}
		events = append(events, OrderBookEvent{
			OrderBook: coincheck.OrderBookData{
				PairStr: pair,
				Bids:    bids,
				Asks:    asks,
			},
			Time: t,
		})
	}

	return events, nil
}
