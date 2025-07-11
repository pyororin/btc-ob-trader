// Package obi_test contains tests for the OBI calculation logic.
package obi_test

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/your-org/obi-scalp-bot/internal/exchange/coincheck"
	"github.com/your-org/obi-scalp-bot/pkg/obi"
)

func TestCalculateOBI(t *testing.T) {
	// Helper to create OrderBookUpdate for tests
	newOrderBookUpdate := func(bids, asks [][]string, lastUpdateAt string) coincheck.OrderBookUpdate {
		data := map[string]interface{}{
			"bids":           make([]interface{}, len(bids)),
			"asks":           make([]interface{}, len(asks)),
			"last_update_at": lastUpdateAt,
		}
		for i, bid := range bids {
			data["bids"].([]interface{})[i] = []interface{}{bid[0], bid[1]}
		}
		for i, ask := range asks {
			data["asks"].([]interface{})[i] = []interface{}{ask[0], ask[1]}
		}
		return coincheck.OrderBookUpdate{"btc_jpy", data}
	}

	tests := []struct {
		name          string
		orderBook     coincheck.OrderBookUpdate
		levels        []int
		expected      obi.OBIResult
		expectError   bool
		checkTimeZero bool // True if we expect the timestamp to be the zero value of time.Time
	}{
		{
			name:      "Empty Order Book",
			orderBook: newOrderBookUpdate([][]string{}, [][]string{}, "1678886400"), // Empty bids and asks
			levels:    []int{8, 16},
			expected: obi.OBIResult{
				OBI8:      0,
				OBI16:     0,
				Timestamp: time.Unix(1678886400, 0),
			},
			expectError: false,
		},
		{
			name: "Bids Only",
			orderBook: newOrderBookUpdate(
				[][]string{{"100", "10"}, {"99", "5"}}, // Bids
				[][]string{},                           // Empty asks
				"1678886401",
			),
			levels: []int{8, 16},
			expected: obi.OBIResult{
				OBI8:      1, // (10+5 - 0) / (10+5 + 0) = 1
				OBI16:     1,
				Timestamp: time.Unix(1678886401, 0),
			},
			expectError: false,
		},
		{
			name: "Asks Only",
			orderBook: newOrderBookUpdate(
				[][]string{},                           // Empty bids
				[][]string{{"101", "8"}, {"102", "7"}}, // Asks
				"1678886402",
			),
			levels: []int{8, 16},
			expected: obi.OBIResult{
				OBI8:      -1, // (0 - (8+7)) / (0 + (8+7)) = -1
				OBI16:     -1,
				Timestamp: time.Unix(1678886402, 0),
			},
			expectError: false,
		},
		{
			name: "Balanced Order Book - Full Levels",
			orderBook: newOrderBookUpdate(
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
			expected: obi.OBIResult{
				OBI8:      0, // (24 - 24) / (24 + 24) = 0
				Timestamp: time.Unix(1678886403, 0),
			},
			expectError: false,
		},
		{
			name: "More Bids than Asks - OBI8 and OBI16",
			orderBook: newOrderBookUpdate(
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
			expected: obi.OBIResult{
				// OBI8: Bids (10+5+3+2+1+1+1+1)=24, Asks (2+2+2+2+2)=10. (24-10)/(24+10) = 14/34
				OBI8: 14.0 / 34.0,
				// OBI16: Bids (10+5+3+2+1+1+1+1+1+1)=26, Asks (2+2+2+2+2)=10. (26-10)/(26+10) = 16/36
				OBI16:     16.0 / 36.0,
				Timestamp: time.Unix(1678886404, 0),
			},
			expectError: false,
		},
		{
			name: "Fewer Bids/Asks than Levels",
			orderBook: newOrderBookUpdate(
				[][]string{{"100", "10"}}, // 1 bid
				[][]string{{"101", "5"}},  // 1 ask
				"1678886405",
			),
			levels: []int{8, 16},
			expected: obi.OBIResult{
				OBI8:      (10.0 - 5.0) / (10.0 + 5.0), // (10-5)/(10+5) = 5/15
				OBI16:     (10.0 - 5.0) / (10.0 + 5.0), // (10-5)/(10+5) = 5/15
				Timestamp: time.Unix(1678886405, 0),
			},
			expectError: false,
		},
		{
			name: "Invalid Data Format - Malformed OrderBookUpdate",
			// Pass a deliberately malformed structure, e.g., not enough elements
			orderBook:     coincheck.OrderBookUpdate{"btc_jpy"}, // Missing the data map
			levels:        []int{8, 16},
			expected:      obi.OBIResult{}, // Expect zero OBIResult
			expectError:   false,           // CalculateOBI currently returns nil error and zero OBIResult
			checkTimeZero: true,            // Expect timestamp to be zero because data extraction fails
		},
		{
			name: "Invalid LastUpdateAt",
			orderBook: newOrderBookUpdate(
				[][]string{{"100", "10"}},
				[][]string{{"101", "5"}},
				"invalid-timestamp", // Invalid timestamp string
			),
			levels: []int{8, 16},
			expected: obi.OBIResult{ // OBI values should still be calculated
				OBI8:  (10.0 - 5.0) / (10.0 + 5.0),
				OBI16: (10.0 - 5.0) / (10.0 + 5.0),
				// Timestamp will be time.Now(), so we can't directly compare it.
				// We'll check its non-zero value separately.
			},
			expectError: false,
		},
		{
			name: "Zero amounts in book levels",
			orderBook: newOrderBookUpdate(
				[][]string{{"100", "10"}, {"99", "0"}}, // Bid with zero amount
				[][]string{{"101", "5"}, {"102", "0"}}, // Ask with zero amount
				"1678886406",
			),
			levels: []int{8, 16},
			expected: obi.OBIResult{
				OBI8:      (10.0 - 5.0) / (10.0 + 5.0), // Zero amounts should be ignored
				OBI16:     (10.0 - 5.0) / (10.0 + 5.0),
				Timestamp: time.Unix(1678886406, 0),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := obi.CalculateOBI(tt.orderBook, tt.levels...)

			if (err != nil) != tt.expectError {
				t.Errorf("CalculateOBI() error = %v, expectError %v", err, tt.expectError)
				return
			}

			// For the "Invalid LastUpdateAt" case, we check if the timestamp is recent
			// instead of comparing to a fixed expected timestamp.
			if tt.name == "Invalid LastUpdateAt" {
				if time.Since(actual.Timestamp) > 5*time.Second { // Allow some slack
					t.Errorf("Expected a recent timestamp for invalid LastUpdateAt, got %v", actual.Timestamp)
				}
				// Set actual.Timestamp to expected.Timestamp for the rest of the comparison
				// Or, better, create a copy of expected and set its timestamp to actual's
				expectedCopy := tt.expected
				expectedCopy.Timestamp = actual.Timestamp
				if !cmp.Equal(expectedCopy, actual, cmpopts.EquateApprox(0.000001, 0)) {
					t.Errorf("CalculateOBI() got = %v, want %v, diff: %s", actual, expectedCopy, cmp.Diff(expectedCopy, actual, cmpopts.EquateApprox(0.000001, 0)))
				}
			} else if tt.checkTimeZero {
				if !actual.Timestamp.IsZero() {
					t.Errorf("Expected Timestamp to be zero, got %v", actual.Timestamp)
				}
				// Compare other fields
				expectedCopy := tt.expected
				expectedCopy.Timestamp = actual.Timestamp // Match the (zero) timestamp for comparison
				if !cmp.Equal(expectedCopy, actual, cmpopts.EquateApprox(0.000001, 0)) {
					t.Errorf("CalculateOBI() got = %v, want %v, diff: %s", actual, expectedCopy, cmp.Diff(expectedCopy, actual, cmpopts.EquateApprox(0.000001, 0)))
				}
			} else {
				if !cmp.Equal(tt.expected, actual, cmpopts.EquateApprox(0.000001, 0)) {
					t.Errorf("CalculateOBI() got = %v, want %v, diff: %s", actual, tt.expected, cmp.Diff(tt.expected, actual, cmpopts.EquateApprox(0.000001, 0)))
				}
			}
		})
	}
}
