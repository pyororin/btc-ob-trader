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
	"fmt"
	"math"

	"github.com/rodrigo-brito/hurst"
)

// CalculateRealizedVolatility calculates the realized volatility of a series of prices.
// It is defined as the standard deviation of the log returns.
func CalculateRealizedVolatility(prices []float64) float64 {
	if len(prices) < 2 {
		return 0.0
	}

	var logReturns []float64
	for i := 1; i < len(prices); i++ {
		if prices[i-1] == 0 {
			continue
		}
		logReturn := math.Log(prices[i] / prices[i-1])
		logReturns = append(logReturns, logReturn)
	}

	if len(logReturns) == 0 {
		return 0.0
	}

	// Calculate the mean of log returns
	var sum float64
	for _, lr := range logReturns {
		sum += lr
	}
	mean := sum / float64(len(logReturns))

	// Calculate the variance of log returns
	var variance float64
	for _, lr := range logReturns {
		variance += math.Pow(lr-mean, 2)
	}
	variance /= float64(len(logReturns))

	// Volatility is the square root of the variance
	return math.Sqrt(variance)
}

// CalculateHurstExponent calculates the Hurst exponent of a series of prices.
// It uses the github.com/rodrigo-brito/hurst library.
func CalculateHurstExponent(prices []float64, minLag, maxLag int) (float64, error) {
	if len(prices) < maxLag {
		return 0.0, fmt.Errorf("not enough data to calculate Hurst exponent, got %d, need at least %d", len(prices), maxLag)
	}
	// The library does not return an error, so we match the signature by returning a nil error.
	return hurst.Estimate(prices, minLag, maxLag), nil
}
