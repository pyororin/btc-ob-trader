// Copyright (c) 2024 OBI-Scalp-Bot
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package indicator

import (
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal {
	res, err := decimal.NewFromString(s)
	if err != nil {
		panic(err)
	}
	return res
}

func TestOFICalculator_UpdateAndCalculateOFI(t *testing.T) {
	tests := []struct {
		name                string
		initialPrevBidPrice string
		initialPrevAskPrice string
		initialPrevBidSize  string
		initialPrevAskSize  string
		updates             []struct {
			bidPrice string
			askPrice string
			bidSize  string
			askSize  string
			expected string // Expected OFI
		}
	}{
		{
			name: "No change initially",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				{"100", "101", "1", "1", "0"}, // Initial update, OFI is (1-0) - (1-0) = 0 if prev sizes were 0
			},
		},
		{
			name: "Price and size increase on bid",
			initialPrevBidPrice: "100", initialPrevAskPrice: "101",
			initialPrevBidSize: "1", initialPrevAskSize: "1",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				{"100.5", "101", "1.5", "1", "1.5"}, // Bid Price Ticks Up: deltaBid = 1.5; Ask Unchanged: deltaAsk = 1-1=0. OFI = 1.5 - 0 = 1.5
			},
		},
		{
			name: "Price and size decrease on ask",
			initialPrevBidPrice: "100", initialPrevAskPrice: "101",
			initialPrevBidSize: "1", initialPrevAskSize: "1",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				{"100", "100.5", "1", "1.5", "-1.5"}, // Ask Price Ticks Down: deltaAsk = 1.5; Bid Unchanged: deltaBid = 1-1=0. OFI = 0 - 1.5 = -1.5
			},
		},
		{
			name: "Bid price down, Ask price up (widening spread)",
			initialPrevBidPrice: "100", initialPrevAskPrice: "101",
			initialPrevBidSize: "1", initialPrevAskSize: "1",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				// Bid Price Ticks Down: deltaBid = -1 (prevBidSize.Neg())
				// Ask Price Ticks Up: deltaAsk = -1 (prevAskSize.Neg())
				// OFI = -1 - (-1) = 0
				{"99.5", "101.5", "0.5", "0.5", "0"},
			},
		},
		{
			name: "Sizes change, prices constant",
			initialPrevBidPrice: "100", initialPrevAskPrice: "101",
			initialPrevBidSize: "1", initialPrevAskSize: "1",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				// Bid Price Constant: deltaBid = 2-1 = 1
				// Ask Price Constant: deltaAsk = 0.5-1 = -0.5
				// OFI = 1 - (-0.5) = 1.5
				{"100", "101", "2", "0.5", "1.5"},
			},
		},
		{
			name: "Complex sequence",
			updates: []struct {
				bidPrice string
				askPrice string
				bidSize  string
				askSize  string
				expected string
			}{
				// Initial state: all prev are 0 or uninitialized
				// Update 1: Prices 100/101, Sizes 1/1.
				// prevBidPrice=0, prevAskPrice=0 (or some other initial non-equal to current)
				// deltaBid: currentBidPrice(100) > prevBidPrice(0) -> deltaBid = currentBidSize(1)
				// deltaAsk: currentAskPrice(101) > prevAskPrice(0) -> deltaAsk = currentAskSize(1) (assuming 0 is less than 101, which is true for price)
				// However, the rule for Ask is: if current < prev, delta = current. if current > prev, delta = -prev.
				// If prevAskPrice is 0, currentAskPrice (101) > prevAskPrice (0). So deltaAsk = -prevAskSize (0) = 0
				// This interpretation depends on how initial prevAskPrice=0 is handled.
				// Let's assume first call initializes prev values and OFI is based on *change* from a known previous state.
				// So, first call to UpdateAndCalculateOFI will use its input to set the "previous" state for the *next* call.
				// The OFI for the first data point itself might be considered 0 or based on arbitrary prior.
				// For testing, it's cleaner to set an initial state for the calculator.
				// Test case "No change initially" handles the first update where prevs are zero. deltaBid = 1, deltaAsk = 1, OFI = 0
				{"100", "101", "1", "1", "0"}, // prevs are zero. dBid=1, dAsk=1. OFI=0. prevs become 100,101,1,1
				// Update 2: Prices 100.5/101, Sizes 1.5/1. prevs: 100,101,1,1
				// deltaBid: 100.5 > 100 -> dBid = 1.5
				// deltaAsk: 101 == 101 -> dAsk = 1 (current) - 1 (prev) = 0
				// OFI = 1.5 - 0 = 1.5. prevs become 100.5,101,1.5,1
				{"100.5", "101", "1.5", "1", "1.5"},
				// Update 3: Prices 100.5/100.5, Sizes 1.5/1.5. prevs: 100.5,101,1.5,1
				// deltaBid: 100.5 == 100.5 -> dBid = 1.5 (current) - 1.5 (prev) = 0
				// deltaAsk: 100.5 < 101 -> dAsk = 1.5 (current)
				// OFI = 0 - 1.5 = -1.5. prevs become 100.5,100.5,1.5,1.5
				{"100.5", "100.5", "1.5", "1.5", "-1.5"},
				// Update 4: Prices 100/101, Sizes 1/1. prevs: 100.5,100.5,1.5,1.5
				// deltaBid: 100 < 100.5 -> dBid = -1.5 (prev.Neg())
				// deltaAsk: 101 > 100.5 -> dAsk = -1.5 (prev.Neg())
				// OFI = -1.5 - (-1.5) = 0. prevs become 100,101,1,1
				{"100", "101", "1", "1", "0"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewOFICalculator()
			if tt.initialPrevBidPrice != "" { // Set initial state if provided
				calc.prevBestBidPrice = d(tt.initialPrevBidPrice)
				calc.prevBestAskPrice = d(tt.initialPrevAskPrice)
				calc.prevBestBidSize = d(tt.initialPrevBidSize)
				calc.prevBestAskSize = d(tt.initialPrevAskSize)
				calc.isInitialized = true // Mark as initialized to bypass the first-call-zero-return logic
			}

			for i, u := range tt.updates {
				bidPrice := d(u.bidPrice)
				askPrice := d(u.askPrice)
				bidSize := d(u.bidSize)
				askSize := d(u.askSize)
				expectedOFI := d(u.expected)

				actualOFI := calc.UpdateAndCalculateOFI(bidPrice, askPrice, bidSize, askSize)

				if !actualOFI.Equal(expectedOFI) {
					t.Errorf("Update %d: UpdateAndCalculateOFI(%s,%s,%s,%s) = %s; want %s. Prev state: bidP=%s, askP=%s, bidS=%s, askS=%s",
						i, u.bidPrice, u.askPrice, u.bidSize, u.askSize, actualOFI.String(), expectedOFI.String(),
						calc.prevBestBidPrice.String(), calc.prevBestAskPrice.String(), calc.prevBestBidSize.String(), calc.prevBestAskSize.String())
				}
			}
		})
	}
}
