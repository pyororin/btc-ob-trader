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
	"math"
	"testing"
)

func TestCalculateMicroPrice(t *testing.T) {
	tests := []struct {
		name     string
		bestBid  float64
		bestAsk  float64
		bidSize  float64
		askSize  float64
		expected float64
	}{
		{
			name:     "Normal case",
			bestBid:  100.0,
			bestAsk:  101.0,
			bidSize:  1.0,
			askSize:  1.0,
			expected: 100.5,
		},
		{
			name:     "Zero bid size",
			bestBid:  100.0,
			bestAsk:  101.0,
			bidSize:  0.0,
			askSize:  1.0,
			expected: 100.0,
		},
		{
			name:     "Zero ask size",
			bestBid:  100.0,
			bestAsk:  101.0,
			bidSize:  1.0,
			askSize:  0.0,
			expected: 101.0,
		},
		{
			name:     "Zero sizes",
			bestBid:  100.0,
			bestAsk:  101.0,
			bidSize:  0.0,
			askSize:  0.0,
			expected: 0.0,
		},
		{
			name:     "Larger bid size",
			bestBid:  7000000.0,
			bestAsk:  7000001.0,
			bidSize:  2.5,
			askSize:  0.5,
			// (7000000.0 * 0.5 + 7000001.0 * 2.5) / (2.5 + 0.5)
			// = (3500000 + 17500002.5) / 3
			// = 21000002.5 / 3 = 7000000.8333...
			expected: 7000000.833333333,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := CalculateMicroPrice(tt.bestBid, tt.bestAsk, tt.bidSize, tt.askSize)
			if math.Abs(actual-tt.expected) > 1e-9 { // Epsilon for float comparison
				t.Errorf("CalculateMicroPrice(%v, %v, %v, %v) = %v; want %v",
					tt.bestBid, tt.bestAsk, tt.bidSize, tt.askSize, actual, tt.expected)
			}
		})
	}
}
