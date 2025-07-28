package indicator_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
)

// newOrderBookData is a helper function to create OrderBookData for tests.
func newOrderBookData(bids, asks [][]string, timestamp string) coincheck.OrderBookData {
	return coincheck.OrderBookData{
		Bids:         bids,
		Asks:         asks,
		LastUpdateAt: timestamp,
	}
}

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
				Timestamp: time.Unix(1678886400, 0),
			},
		},
		{
			name: "Bids Only",
			snapshot: newOrderBookData(
				[][]string{{"100", "10"}, {"99", "5"}},
				[][]string{},
				"1678886401",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				BestBid:   100,
				Timestamp: time.Unix(1678886401, 0),
			},
		},
		{
			name: "Asks Only",
			snapshot: newOrderBookData(
				[][]string{},
				[][]string{{"101", "8"}, {"102", "7"}},
				"1678886402",
			),
			levels: []int{8, 16},
			expected: indicator.OBIResult{
				BestAsk:   101,
				Timestamp: time.Unix(1678886402, 0),
			},
		},
		{
			name: "Balanced Order Book - Full Levels",
			snapshot: newOrderBookData(
				[][]string{
					{"100", "10"}, {"99", "5"}, {"98", "3"}, {"97", "2"},
					{"96", "1"}, {"95", "1"}, {"94", "1"}, {"93", "1"},
				},
				[][]string{
					{"101", "10"}, {"102", "5"}, {"103", "3"}, {"104", "2"},
					{"105", "1"}, {"106", "1"}, {"107", "1"}, {"108", "1"},
				},
				"1678886403",
			),
			levels: []int{8},
			expected: indicator.OBIResult{
				OBI8:      0,
				BestBid:   100,
				BestAsk:   101,
				Timestamp: time.Unix(1678886403, 0),
			},
		},
		{
			name: "More Bids than Asks - OBI8 and OBI16",
			snapshot: newOrderBookData(
				[][]string{
					{"100", "10"}, {"99", "5"}, {"98", "3"}, {"97", "2"},
					{"96", "1"}, {"95", "1"}, {"94", "1"}, {"93", "1"},
					{"92", "1"}, {"91", "1"},
				},
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
				[][]string{{"100", "10"}},
				[][]string{{"101", "5"}},
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
				[][]string{{"100", "10"}, {"99", "0"}},
				[][]string{{"101", "5"}, {"102", "0"}},
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
			actual, ok := ob.CalculateOBI(tt.levels...)

			isEmptyOrOneSided := len(tt.snapshot.Bids) == 0 || len(tt.snapshot.Asks) == 0
			if isEmptyOrOneSided {
				if ok {
					t.Fatalf("CalculateOBI() returned ok=true for an empty or one-sided book")
				}
				tt.expected.Timestamp = actual.Timestamp
			} else {
				if !ok {
					t.Fatalf("CalculateOBI() returned ok=false for a valid order book")
				}
			}

			expected := tt.expected
			contains16 := false
			for _, l := range tt.levels {
				if l == 16 {
					contains16 = true
					break
				}
			}
			if !contains16 {
				expected.OBI16 = actual.OBI16
			}

			if !cmp.Equal(expected, actual, cmpopts.EquateApprox(0.000001, 0)) {
				t.Errorf("CalculateOBI() got = %v, want %v, diff: %s", actual, expected, cmp.Diff(expected, actual, cmpopts.EquateApprox(0.000001, 0)))
			}
		})
	}
}

func TestOrderBook_ApplyUpdate(t *testing.T) {
	ob := indicator.NewOrderBook()

	initialData := newOrderBookData([][]string{{"100", "10"}}, [][]string{{"101", "5"}}, "1678886400")
	ob.ApplySnapshot(initialData)

	updateData := newOrderBookData([][]string{{"200", "20"}}, [][]string{{"201", "15"}}, "1678886401")
	ob.ApplyUpdate(updateData)

	expected := indicator.OBIResult{
		OBI8:      (20.0 - 15.0) / (20.0 + 15.0),
		OBI16:     (20.0 - 15.0) / (20.0 + 15.0),
		BestBid:   200,
		BestAsk:   201,
		Timestamp: time.Unix(1678886401, 0),
	}
	actual, ok := ob.CalculateOBI(8, 16)
	if !ok {
		t.Fatalf("CalculateOBI() returned ok=false unexpectedly")
	}

	if !cmp.Equal(expected, actual, cmpopts.EquateApprox(0.000001, 0)) {
		t.Errorf("CalculateOBI() after ApplyUpdate got = %v, want %v, diff: %s", actual, expected, cmp.Diff(expected, actual, cmpopts.EquateApprox(0.000001, 0)))
	}
}
