package datastore

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// FetchMarketEventsFromCSV reads order book update data from a CSV file,
// groups the updates by timestamp to form order book snapshots,
// and returns them as a sorted slice of MarketEvent.
// The CSV file is expected to have a header and the following columns:
// time, pair, side, price, size, is_snapshot
func FetchMarketEventsFromCSV(ctx context.Context, filePath string) ([]MarketEvent, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open csv file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Read the header row
	if _, err := reader.Read(); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("csv file is empty or contains only a header")
		}
		return nil, fmt.Errorf("failed to read csv header: %w", err)
	}

	// Group updates by timestamp
	type bookUpdate struct {
		Side  string
		Price float64
		Size  float64
		Pair  string
	}
	updatesByTime := make(map[time.Time][]bookUpdate)
	var timeOrder []time.Time

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read csv record: %w", err)
		}

		// --- Parse record ---
		// time, pair, side, price, size, is_snapshot
		if len(record) != 6 {
			logger.Warnf("Skipping record due to invalid number of columns: expected 6, got %d", len(record))
			continue
		}

		eventTime, err := parseTime(record[0])
		if err != nil {
			logger.Warnf("Skipping record due to time parse error: %v", err)
			continue
		}
		pair := record[1]
		side := record[2]
		price, err := strconv.ParseFloat(record[3], 64)
		if err != nil {
			logger.Warnf("Skipping record due to price parse error: %v, record: %v", err, record)
			continue
		}
		size, err := strconv.ParseFloat(record[4], 64)
		if err != nil {
			logger.Warnf("Skipping record due to size parse error: %v, record: %v", err, record)
			continue
		}
		// is_snapshot (record[5]) is ignored for now, as we treat each timestamp group as a snapshot.

		if _, exists := updatesByTime[eventTime]; !exists {
			timeOrder = append(timeOrder, eventTime)
		}
		updatesByTime[eventTime] = append(updatesByTime[eventTime], bookUpdate{
			Side:  side,
			Price: price,
			Size:  size,
			Pair:  pair,
		})
	}

	// Sort timestamps to process events in chronological order
	sort.Slice(timeOrder, func(i, j int) bool {
		return timeOrder[i].Before(timeOrder[j])
	})

	var events []MarketEvent
	for _, t := range timeOrder {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			updates := updatesByTime[t]
			var bids, asks [][]string
			var pair string
			for _, u := range updates {
				level := []string{fmt.Sprintf("%f", u.Price), fmt.Sprintf("%f", u.Size)}
				if u.Side == "bid" {
					bids = append(bids, level)
				} else {
					asks = append(asks, level)
				}
				if pair == "" {
					pair = u.Pair // Assume all updates in a snapshot are for the same pair
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
	}

	logger.Infof("Successfully loaded %d order book snapshots from %s", len(events), filePath)
	return events, nil
}

func parseTime(timeStr string) (time.Time, error) {
	// First, try RFC3339 format
	t, err := time.Parse(time.RFC3339, timeStr)
	if err == nil {
		return t, nil
	}
	// Fallback to psql's default format with space and timezone
	// e.g., "2025-07-14 04:11:13.484971+00"
	layout := "2006-01-02 15:04:05.999999-07"
	t, err = time.Parse(layout, timeStr)
	if err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("could not parse time '%s' with any known format", timeStr)
}
