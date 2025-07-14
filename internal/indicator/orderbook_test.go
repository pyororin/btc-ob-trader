package indicator_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
)

func TestOrderBook_ApplySnapshotAndCalculateOBI(t *testing.T) {
	tests := []struct {
		name     string
		snapshot coincheck.OrderBookData
		levels   []int
		expected indicator.OBIResult
	}{
		{
			name:     "Empty Order Book",
			snapshot: newOrderBookData([][]string{}, [][]string{}, "1678886400"),
			levels:   []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      0,
				OBI16:     0,
				BestBid:   0,
				BestAsk:   0,
				Timestamp: time.Unix(1678886400, 0),
			},
		},
		{
			name: "Bids Only",
			snapshot: newOrderBookData(
				[][]string{{"100", "10"}, {"99", "5"}}, // Bids
				[][]string{}, // Empty asks
				"1678886401",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      1, // (10+5 - 0) / (10+5 + 0) = 1
				OBI16:     1,
				BestBid:   100,
				BestAsk:   0,
				Timestamp: time.Unix(1678886401, 0),
			},
		},
		{
			name: "Asks Only",
			snapshot: newOrderBookData(
				[][]string{}, // Empty bids
				[][]string{{"101", "8"}, {"102", "7"}}, // Asks
				"1678886402",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      -1, // (0 - (8+7)) / (0 + (8+7)) = -1
				OBI16:     -1,
				BestBid:   0,
				BestAsk:   101,
				Timestamp: time.Unix(1678886402, 0),
			},
		},
		{
			name: "Balanced Order Book - Full Levels",
			snapshot: newOrderBookData(
				[][]string{
					{"100", "10"}, {"99", "5"}, {"98", "3"}, {"97", "2"},
					{"96", "1"}, {"95", "1"}, {"94", "1"}, {"93", "1"}, // 8 bids
				},
				[][]string{
					{"101", "10"}, {"102", "5"}, {"103", "3"}, {"104", "2"},
					{"105", "1"}, {"106", "1"}, {"107", "1"}, {"108", "1"}, // 8 asks
				},
				"1678886403",
			),
			levels: []int{8},
			expected: indicator.OBIResult{
				OBI8:      0, // (24 - 24) / (24 + 24) = 0
				BestBid:   100,
				BestAsk:   101,
				Timestamp: time.Unix(1678886403, 0),
			},
		},
		{
			name: "More Bids than Asks - OBI8 and OBI16",
			snapshot: newOrderBookData(
				// 10 bids
				[][]string{
					{"100", "10"}, {"99", "5"}, {"98", "3"}, {"97", "2"},
					{"96", "1"}, {"95", "1"}, {"94", "1"}, {"93", "1"},
					{"92", "1"}, {"91", "1"},
				},
				// 5 asks
				[][]string{
					{"101", "2"}, {"102", "2"}, {"103", "2"}, {"104", "2"},
					{"105", "2"},
				},
				"1678886404",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      14.0 / 34.0,
				OBI16:     16.0 / 36.0,
				BestBid:   100,
				BestAsk:   101,
				Timestamp: time.Unix(1678886404, 0),
			},
		},
		{
			name: "Fewer Bids/Asks than Levels",
			snapshot: newOrderBookData(
				[][]string{{"100", "10"}}, // 1 bid
				[][]string{{"101", "5"}},  // 1 ask
				"1678886405",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      (10.0 - 5.0) / (10.0 + 5.0),
				OBI16:     (10.0 - 5.0) / (10.0 + 5.0),
				BestBid:   100,
				BestAsk:   101,
				Timestamp: time.Unix(1678886405, 0),
			},
		},
		{
			name: "Zero amounts in book levels",
			snapshot: newOrderBookData(
				[][]string{{"100", "10"}, {"99", "0"}}, // Bid with zero amount
				[][]string{{"101", "5"}, {"102", "0"}}, // Ask with zero amount
				"1678886406",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				OBI8:      (10.0 - 5.0) / (10.0 + 5.0),
				OBI16:     (10.0 - 5.0) / (10.0 + 5.0),
				BestBid:   100,
				BestAsk:   101,
				Timestamp: time.Unix(1678886406, 0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ob := indicator.NewOrderBook()
			ob.ApplySnapshot(tt.snapshot)
			actual := ob.CalculateOBI(tt.levels...)

			// We need to handle the case where OBI16 might not be calculated
			// if it's not in tt.levels.
			expected := tt.expected
			contains16 := false
			for _, l := range tt.levels {
				if l == 16 {
					contains16 = true
					break
				}
			}
			if !contains16 {
				// If we didn't ask for OBI16, the result won't have it.
				// We should not compare it.
				// A simple way is to set the expected OBI16 to the actual OBI16.
				expected.OBI16 = actual.OBI16
			}

			if !cmp.Equal(expected, actual, cmpopts.EquateApprox(0.000001, 0)) {
				t.Errorf("CalculateOBI() got = %v, want %v, diff: %s", actual, expected, cmp.Diff(expected, actual, cmpopts.EquateApprox(0.000001, 0)))
			}
		})
	}
}

func TestOrderBook_ApplyUpdate(t *testing.T) {
	// Since ApplyUpdate just calls ApplySnapshot for Coincheck, we'll do a simple test
	// to ensure it correctly replaces the book content.
	ob := indicator.NewOrderBook()

	// Initial state
	initialData := newOrderBookData([][]string{{"100", "10"}}, [][]string{{"101", "5"}}, "1678886400")
	ob.ApplySnapshot(initialData)

	// Update
	updateData := newOrderBookData([][]string{{"200", "20"}}, [][]string{{"201", "15"}}, "1678886401")
	ob.ApplyUpdate(updateData)

	// Calculate OBI and check
	expected := indicator.OBIResult{
		OBI8:      (20.0 - 15.0) / (20.0 + 15.0),
		OBI16:     (20.0 - 15.0) / (20.0 + 15.0),
		BestBid:   200,
		BestAsk:   201,
		Timestamp: time.Unix(1678886401, 0),
	}
	actual := ob.CalculateOBI(8, 16)

	if !cmp.Equal(expected, actual, cmpopts.EquateApprox(0.000001, 0)) {
		t.Errorf("CalculateOBI() after ApplyUpdate got = %v, want %v, diff: %s", actual, expected, cmp.Diff(expected, actual, cmpopts.EquateApprox(0.000001, 0)))
	}
}
