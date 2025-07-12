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
	"github.com/shopspring/decimal"
)

// OFICalculator calculates Order Flow Imbalance (OFI).
// It tracks the change in best bid and ask prices and sizes.
type OFICalculator struct {
	prevBestBidPrice decimal.Decimal
	prevBestAskPrice decimal.Decimal
	prevBestBidSize  decimal.Decimal
	prevBestAskSize  decimal.Decimal
	isInitialized    bool
}

// NewOFICalculator creates a new OFICalculator.
func NewOFICalculator() *OFICalculator {
	return &OFICalculator{}
}

// UpdateAndCalculateOFI updates the state with the current best bid/ask
// and calculates OFI based on the changes from the previous state.
// The first call initializes the state and returns zero.
//
// OFI is calculated based on the rules:
// - If bid price increases: delta bid size is current bid size.
// - If bid price decreases: delta bid size is -previous bid size.
// - If bid price is same: delta bid size is current bid size - previous bid size.
// - Similar rules apply for ask side.
//
// Returns the calculated OFI.
func (c *OFICalculator) UpdateAndCalculateOFI(
	currentBestBidPrice, currentBestAskPrice,
	currentBestBidSize, currentBestAskSize decimal.Decimal,
) decimal.Decimal {
	if !c.isInitialized {
		c.prevBestBidPrice = currentBestBidPrice
		c.prevBestAskPrice = currentBestAskPrice
		c.prevBestBidSize = currentBestBidSize
		c.prevBestAskSize = currentBestAskSize
		c.isInitialized = true
		return decimal.Zero
	}

	var deltaBidSize, deltaAskSize decimal.Decimal

	// Calculate delta for bid side
	if currentBestBidPrice.GreaterThan(c.prevBestBidPrice) {
		deltaBidSize = currentBestBidSize
	} else if currentBestBidPrice.LessThan(c.prevBestBidPrice) {
		deltaBidSize = c.prevBestBidSize.Neg()
	} else { // currentBestBidPrice.Equal(c.prevBestBidPrice)
		deltaBidSize = currentBestBidSize.Sub(c.prevBestBidSize)
	}

	// Calculate delta for ask side
	if currentBestAskPrice.LessThan(c.prevBestAskPrice) {
		deltaAskSize = currentBestAskSize
	} else if currentBestAskPrice.GreaterThan(c.prevBestAskPrice) {
		deltaAskSize = c.prevBestAskSize.Neg()
	} else { // currentBestAskPrice.Equal(c.prevBestAskPrice)
		deltaAskSize = currentBestAskSize.Sub(c.prevBestAskSize)
	}

	// Update previous state for the next calculation
	c.prevBestBidPrice = currentBestBidPrice
	c.prevBestAskPrice = currentBestAskPrice
	c.prevBestBidSize = currentBestBidSize
	c.prevBestAskSize = currentBestAskSize

	// OFI = Delta Bid Size - Delta Ask Size
	return deltaBidSize.Sub(deltaAskSize)
}
