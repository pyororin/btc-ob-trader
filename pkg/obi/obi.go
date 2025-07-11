// Package obi provides types and functions for calculating Order Book Imbalance (OBI).
package obi

import (
	"container/heap"
	"strconv"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
)

// OBIResult holds the calculated OBI values for different levels and the timestamp.
type OBIResult struct {
	OBI8      float64   // Order Book Imbalance for top 8 levels
	OBI16     float64   // Order Book Imbalance for top 16 levels
	Timestamp time.Time // Timestamp of the OBI calculation (derived from last_update_at or current time)
}

// PriceLevel represents a price level with rate and amount.
// Used for heap implementation.
type PriceLevel struct {
	Rate   float64
	Amount float64
	IsBid  bool // true for bids (higher rate is better), false for asks (lower rate is better)
}

// PriceLevelHeap implements heap.Interface for a list of PriceLevel.
// For bids, it's a max-heap based on rate.
// For asks, it's a min-heap based on rate.
type PriceLevelHeap []*PriceLevel

func (h PriceLevelHeap) Len() int { return len(h) }
func (h PriceLevelHeap) Less(i, j int) bool {
	// For bids (max-heap): higher rate is "less" (comes earlier)
	// For asks (min-heap): lower rate is "less" (comes earlier)
	if h[i].IsBid {
		return h[i].Rate > h[j].Rate // Max-heap for bids
	}
	return h[i].Rate < h[j].Rate // Min-heap for asks
}
func (h PriceLevelHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// Push and Pop use pointer receivers because they modify the slice's length,
// not just its contents.
func (h *PriceLevelHeap) Push(x interface{}) {
	*h = append(*h, x.(*PriceLevel))
}

func (h *PriceLevelHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// CalculateOBI calculates the Order Book Imbalance for the given levels.
// It takes an OrderBookUpdate (which is []interface{} from Coincheck) and the number of levels for OBI calculation.
func CalculateOBI(orderBookUpdate coincheck.OrderBookUpdate, levels ...int) (OBIResult, error) {
	result := OBIResult{}
	data, ok := orderBookUpdate.Data()
	if !ok {
		// If data extraction fails, return an empty result or an error
		// For now, let's return an empty result and rely on Timestamp being zero.
		// Consider returning an error if this case needs specific handling.
		return result, nil // Or an error indicating invalid data format
	}

	if data.LastUpdateAt != "" {
		ts, err := strconv.ParseInt(data.LastUpdateAt, 10, 64)
		if err == nil {
			result.Timestamp = time.Unix(ts, 0)
		} else {
			result.Timestamp = time.Now().UTC() // Fallback if parsing fails
		}
	} else {
		result.Timestamp = time.Now().UTC() // Fallback if LastUpdateAt is empty
	}

	bidHeap := &PriceLevelHeap{}
	askHeap := &PriceLevelHeap{}
	heap.Init(bidHeap)
	heap.Init(askHeap)

	for _, b := range data.Bids {
		rate, errRate := strconv.ParseFloat(b[0], 64)
		amount, errAmount := strconv.ParseFloat(b[1], 64)
		if errRate == nil && errAmount == nil && amount > 0 { // Ensure amount is positive
			heap.Push(bidHeap, &PriceLevel{Rate: rate, Amount: amount, IsBid: true})
		}
	}

	for _, a := range data.Asks {
		rate, errRate := strconv.ParseFloat(a[0], 64)
		amount, errAmount := strconv.ParseFloat(a[1], 64)
		if errRate == nil && errAmount == nil && amount > 0 { // Ensure amount is positive
			heap.Push(askHeap, &PriceLevel{Rate: rate, Amount: amount, IsBid: false})
		}
	}

	maxLevel := 0
	for _, l := range levels {
		if l > maxLevel {
			maxLevel = l
		}
	}

	sumBids := make(map[int]float64)
	sumAsks := make(map[int]float64)

	currentBidsTotal := 0.0
	for i := 0; i < maxLevel && bidHeap.Len() > 0; i++ {
		level := heap.Pop(bidHeap).(*PriceLevel)
		currentBidsTotal += level.Amount
		for _, l := range levels {
			if i < l {
				sumBids[l] += level.Amount
			}
		}
	}

	currentAsksTotal := 0.0
	for i := 0; i < maxLevel && askHeap.Len() > 0; i++ {
		level := heap.Pop(askHeap).(*PriceLevel)
		currentAsksTotal += level.Amount
		for _, l := range levels {
			if i < l {
				sumAsks[l] += level.Amount
			}
		}
	}

	for _, l := range levels {
		totalBids := sumBids[l]
		totalAsks := sumAsks[l]
		var obiValue float64
		if totalBids+totalAsks > 0 { // Avoid division by zero
			obiValue = (totalBids - totalAsks) / (totalBids + totalAsks)
		} else {
			obiValue = 0 // Or NaN, depending on desired behavior for empty/zero books
		}

		if l == 8 {
			result.OBI8 = obiValue
		} else if l == 16 {
			result.OBI16 = obiValue
		}
		// Add more cases if other levels are needed
	}

	return result, nil
}
