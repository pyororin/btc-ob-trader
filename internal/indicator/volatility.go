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
)

// VolatilityCalculator calculates EWMA (Exponentially Weighted Moving Average)
// and EWMVar (Exponentially Weighted Moving Variance) of price returns.
type VolatilityCalculator struct {
	lambda      float64 // Decay factor for EWMA, typically close to 1 (e.g., 0.94 or config specified)
	prevPrice   float64
	ewmaReturn  float64
	ewmVarReturn float64
	isInitialized bool
}

// NewVolatilityCalculator creates a new VolatilityCalculator.
// lambda is the decay factor (e.g., config.Volatility.EWMALambda, but adapted for variance calculation if needed).
// For variance, common practice is lambda_var = lambda_price^2 or a separate parameter.
// Here, we assume lambda is for the return's EWMA and EWMVar directly.
func NewVolatilityCalculator(lambda float64) *VolatilityCalculator {
	return &VolatilityCalculator{
		lambda: lambda, // This lambda is for the EWMA of returns and EWMVariance
	}
}

// Update calculates the EWMA of returns and EWMVariance of returns.
// It requires the current price.
// Returns the EWMA of returns and the EWMStdDev (sqrt of EWMVar).
func (vc *VolatilityCalculator) Update(currentPrice float64) (ewmaRet float64, ewmStdDev float64) {
	if !vc.isInitialized {
		vc.prevPrice = currentPrice
		vc.isInitialized = true
		return 0, 0 // Not enough data yet
	}

	if vc.prevPrice == 0 { // Avoid division by zero if previous price was zero
		vc.prevPrice = currentPrice
		return 0,0
	}

	// Calculate log return (ln(currentPrice / prevPrice))
	// Using simple return for now: (currentPrice - vc.prevPrice) / vc.prevPrice
    // For crypto, log returns are often preferred due to large price movements.
    // However, the original config mentions `ewma_lambda` without specifying for price or return.
    // Let's use simple returns first, can be changed to log returns if needed.
	ret := (currentPrice - vc.prevPrice) / vc.prevPrice

	// Update EWMA of returns
	// EWMA_t = lambda * Return_t + (1 - lambda) * EWMA_{t-1}
	// The config `ewma_lambda` seems to be `alpha` in the common (1-alpha) formulation or `1-lambda` in others.
    // Assuming `lambda` from config IS the `alpha` for the current value's weight.
    // So, EWMA_t = configured_lambda * Value_t + (1 - configured_lambda) * EWMA_{t-1}
	vc.ewmaReturn = vc.lambda*ret + (1-vc.lambda)*vc.ewmaReturn

	// Update EWMVar of returns
	// EWMVar_t = lambda * (Return_t - EWMA_{t-1})^2 + (1 - lambda) * EWMVar_{t-1}
	// Note: Using EWMA_{t-1} (the previous EWMA of returns) for the squared difference.
    // Some formulations use EWMA_t (current). RiskMetrics uses 0 for the mean (lambda * R_t^2 + ...).
    // Let's use the EWMA of returns we just calculated (vc.ewmaReturn) as the mean for variance.
    // EWMVar_t = lambda * (Return_t - vc.ewmaReturn)^2 + (1-lambda) * EWMVar_{t-1}
    // No, standard is more like: EWMVar_t = (1-lambda) * EWMVar_{t-1} + lambda * (Return_t - EWMA_{t-1})^2
    // Let's use the common definition where `lambda` is the weight of the new squared deviation.
	// deviation := ret - vc.ewmaReturn // Deviation from the *current* EWMA of returns
    // Or, if we assume mean of returns is zero for variance calculation (common in finance for daily returns):
    // vc.ewmVarReturn = vc.lambda*(ret*ret) + (1-vc.lambda)*vc.ewmVarReturn
    // Let's use the definition for EWMVar where lambda is the weight of the current observation's squared deviation from the EWA of returns.
    // EWMVar_t = alpha * (value_t - EWMA_return_t)^2 + (1-alpha) * EWMVar_{t-1}
    // where EWMA_return_t is the *updated* ewma.
    // This is slightly complex. A simpler, widely used form (e.g. RiskMetrics) assumes mean return is 0 for variance:
    // Var_t = (1-lambda_decay) * Var_{t-1} + lambda_decay * R_t^2
    // Here, `vc.lambda` from config is `lambda_decay`.
	vc.ewmVarReturn = (1-vc.lambda)*vc.ewmVarReturn + vc.lambda*(ret*ret)


	vc.prevPrice = currentPrice

	// Standard deviation is the square root of variance
	ewmStdDev = math.Sqrt(vc.ewmVarReturn)

	return vc.ewmaReturn, ewmStdDev
}

// GetEWMAVolatility returns the current EWMA of price returns (annualized if period is known).
// For now, it returns the raw EWMA of returns per update period.
func (vc *VolatilityCalculator) GetEWMAVolatility() float64 {
	return vc.ewmaReturn
}

// GetEWMStandardDeviation returns the current EWMA Standard Deviation of price returns.
func (vc *VolatilityCalculator) GetEWMStandardDeviation() float64 {
	if !vc.isInitialized || vc.ewmVarReturn < 0 { // Variance can't be negative
		return 0
	}
	return math.Sqrt(vc.ewmVarReturn)
}
