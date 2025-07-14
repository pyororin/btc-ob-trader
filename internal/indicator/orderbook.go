// Package indicator provides types and functions for calculating various market indicators.
package indicator

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// OBIResult holds the calculated OBI values for different levels and the timestamp.
type OBIResult struct {
	OBI8      float64   // Order Book Imbalance for top 8 levels
	OBI16     float64   // Order Book Imbalance for top 16 levels
	BestBid   float64   // Best bid price at the time of calculation
	BestAsk   float64   // Best ask price at the time of calculation
	Timestamp time.Time // Timestamp of the OBI calculation
}

// OrderBook represents a thread-safe order book.
type OrderBook struct {
	sync.RWMutex
	Bids map[float64]float64 // rate -> amount
	Asks map[float64]float64 // rate -> amount
	Time time.Time
}

// NewOrderBook creates and initializes a new OrderBook.
func NewOrderBook() *OrderBook {
	return &OrderBook{
		Bids: make(map[float64]float64),
		Asks: make(map[float64]float64),
	}
}

// parseLevel parses a single level from the order book update.
func parseLevel(level []string) (float64, float64, error) {
	rate, err := strconv.ParseFloat(level[0], 64)
	if err != nil {
		return 0, 0, err
	}
	amount, err := strconv.ParseFloat(level[1], 64)
	if err != nil {
		return 0, 0, err
	}
	return rate, amount, nil
}

// ApplySnapshot clears the existing order book and populates it with data from a snapshot.
func (ob *OrderBook) ApplySnapshot(data coincheck.OrderBookData) {
	ob.Lock()
	defer ob.Unlock()

	ob.Bids = make(map[float64]float64)
	ob.Asks = make(map[float64]float64)

	for _, bid := range data.Bids {
		if rate, amount, err := parseLevel(bid); err == nil {
			ob.Bids[rate] = amount
		}
	}
	for _, ask := range data.Asks {
		if rate, amount, err := parseLevel(ask); err == nil {
			ob.Asks[rate] = amount
		}
	}
	if ts, err := strconv.ParseInt(data.LastUpdateAt, 10, 64); err == nil {
		ob.Time = time.Unix(ts, 0)
	} else {
		ob.Time = time.Now().UTC()
	}
}

// ApplyUpdate applies a differential update to the order book.
// For Coincheck, an "update" is a full snapshot, so this is the same as ApplySnapshot.
// If the exchange provided diffs, this logic would be different.
func (ob *OrderBook) ApplyUpdate(data coincheck.OrderBookData) {
	// Coincheck sends full snapshots for updates, so we just re-apply.
	ob.ApplySnapshot(data)
}

// PriceLevel represents a price level for heap implementation.
type priceLevel struct {
	Rate   float64
	Amount float64
}

// CalculateOBI calculates the Order Book Imbalance from the current state of the book.
func (ob *OrderBook) CalculateOBI(levels ...int) OBIResult {
	ob.RLock()
	defer ob.RUnlock()

	result := OBIResult{Timestamp: ob.Time}

	// Create sorted slices of bids and asks
	bids := make([]priceLevel, 0, len(ob.Bids))
	for rate, amount := range ob.Bids {
		if amount > 0 {
			bids = append(bids, priceLevel{Rate: rate, Amount: amount})
		}
	}
	// Sort bids descending by rate (best bids first)
	sort.Slice(bids, func(i, j int) bool {
		return bids[i].Rate > bids[j].Rate
	})

	asks := make([]priceLevel, 0, len(ob.Asks))
	for rate, amount := range ob.Asks {
		if amount > 0 {
			asks = append(asks, priceLevel{Rate: rate, Amount: amount})
		}
	}
	// Sort asks ascending by rate (best asks first)
	sort.Slice(asks, func(i, j int) bool {
		return asks[i].Rate < asks[j].Rate
	})

	if len(bids) > 0 {
		result.BestBid = bids[0].Rate
	}
	if len(asks) > 0 {
		result.BestAsk = asks[0].Rate
	}

	maxLevel := 0
	for _, l := range levels {
		if l > maxLevel {
			maxLevel = l
		}
	}

	sumBids := make(map[int]float64)
	sumAsks := make(map[int]float64)

	for i := 0; i < len(bids) && i < maxLevel; i++ {
		amount := bids[i].Amount
		for _, l := range levels {
			if i < l {
				sumBids[l] += amount
			}
		}
	}

	for i := 0; i < len(asks) && i < maxLevel; i++ {
		amount := asks[i].Amount
		for _, l := range levels {
			if i < l {
				sumAsks[l] += amount
			}
		}
	}

	for _, l := range levels {
		totalBids := sumBids[l]
		totalAsks := sumAsks[l]
		var obiValue float64
		if totalBids+totalAsks > 0 {
			obiValue = (totalBids - totalAsks) / (totalBids + totalAsks)
		}

		if l == 8 {
			result.OBI8 = obiValue
		} else if l == 16 {
			result.OBI16 = obiValue
		}
	}

	return result
}
