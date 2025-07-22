// Package signal provides the logic for generating trading signals.
package signal

import (
	"fmt"
	"math" // Required for math.Min/Max
	"time"

	"github.com/shopspring/decimal"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/indicator" // Added
	"github.com/your-org/obi-scalp-bot/pkg/logger"
)

// SignalType represents the type of trading signal.
type SignalType int

const (
	// SignalNone indicates no signal.
	SignalNone SignalType = iota
	// SignalLong indicates a long signal.
	SignalLong
	// SignalShort indicates a short signal.
	SignalShort
)

// TradingSignal holds information about a generated trading signal, including TP/SL levels.
type TradingSignal struct {
	Type        SignalType
	EntryPrice  float64 // The price at which the signal was generated or the expected entry price
	TakeProfit  float64 // Absolute price level for take profit
	StopLoss    float64 // Absolute price level for stop loss
	TriggerOBIOBIValue    float64 // OBI value that triggered the signal
	TriggerTime time.Time // Time the signal was confirmed
}

// String returns the string representation of SignalType.
func (s SignalType) String() string {
	switch s {
	case SignalLong:
		return "LONG"
	case SignalShort:
		return "SHORT"
	case SignalNone:
		return "NONE"
	default:
		return "UNKNOWN"
	}
}

// EngineConfig encapsulates configuration for the SignalEngine.
type EngineConfig struct {
	LongOBIBaseThreshold  float64 // Base threshold from config
	ShortOBIBaseThreshold float64 // Base threshold from config
	SignalHoldDuration    time.Duration
	DynamicOBIConf        config.DynamicOBIConf // Added
}

// Regime represents the market regime.
type Regime int

const (
	// RegimeTrending indicates a trending market.
	RegimeTrending Regime = iota
	// RegimeMeanReverting indicates a mean-reverting market.
	RegimeMeanReverting
	// RegimeUnknown indicates an unknown market regime.
	RegimeUnknown
)

// SignalEngine evaluates market data and generates trading signals.
type SignalEngine struct {
	config                   EngineConfig
	volatilityCalc           *indicator.VolatilityCalculator
	ofiCalc                  *indicator.OFICalculator
	lastSignal               SignalType
	lastSignalTime           time.Time
	currentSignal            SignalType
	currentSignalSince       time.Time
	currentLongOBIThreshold  float64
	currentShortOBIThreshold float64
	currentMidPrice          float64
	longTP                   float64
	longSL                   float64
	shortTP                  float64
	shortSL                  float64

	// Fields for OBI slope filter
	slopeFilterConfig config.SlopeFilterConfig
	obiHistory        []float64

	// Fields for regime detection
	priceHistory             []float64
	maxHistory               int
	currentRegime            Regime
	hurstExponent            float64
	lastHurstCalculationTime time.Time
}

// NewSignalEngine creates a new SignalEngine.
func NewSignalEngine(tradeCfg *config.TradeConfig) (*SignalEngine, error) {
	signalHoldDuration := time.Duration(tradeCfg.Signal.HoldDurationMs) * time.Millisecond

	engineCfg := EngineConfig{
		LongOBIBaseThreshold:  tradeCfg.Long.OBIThreshold,
		ShortOBIBaseThreshold: tradeCfg.Short.OBIThreshold,
		SignalHoldDuration:    signalHoldDuration,
		DynamicOBIConf:        tradeCfg.Volatility.DynamicOBI,
	}

	return &SignalEngine{
		config:                   engineCfg,
		volatilityCalc:           indicator.NewVolatilityCalculator(tradeCfg.Volatility.EWMALambda),
		ofiCalc:                  indicator.NewOFICalculator(),
		lastSignal:               SignalNone,
		currentSignal:            SignalNone,
		currentLongOBIThreshold:  engineCfg.LongOBIBaseThreshold,
		currentShortOBIThreshold: engineCfg.ShortOBIBaseThreshold,
		longTP:                   tradeCfg.Long.TP,
		longSL:                   tradeCfg.Long.SL,
		shortTP:                  tradeCfg.Short.TP,
		shortSL:                  tradeCfg.Short.SL,
		slopeFilterConfig:        tradeCfg.Signal.SlopeFilter,
		obiHistory:               make([]float64, 0, tradeCfg.Signal.SlopeFilter.Period),
		priceHistory:             make([]float64, 0, 100),
		maxHistory:               100,
		currentRegime:            RegimeUnknown,
		hurstExponent:            0.0,
		lastHurstCalculationTime: time.Time{},
	}, nil
}

// UpdateMarketData updates the engine with the latest market data including price for volatility,
// and best bid/ask for OFI. This method should be called before Evaluate.
// Parameters like bestBid, bestAsk, bestBidSize, bestAskSize are for OFI.
// currentMidPrice is for volatility and for TP/SL calculation base.
func (e *SignalEngine) UpdateMarketData(currentTime time.Time, currentMidPrice, bestBid, bestAsk, bestBidSize, bestAskSize float64) {
	e.currentMidPrice = currentMidPrice // Store for Evaluate method

	// Update price history
	e.priceHistory = append(e.priceHistory, currentMidPrice)
	if len(e.priceHistory) > e.maxHistory {
		e.priceHistory = e.priceHistory[1:]
	}

	// Update regime, but not on every single tick to save CPU
	const hurstCalculationInterval = 1 * time.Minute
	if len(e.priceHistory) == e.maxHistory && currentTime.Sub(e.lastHurstCalculationTime) > hurstCalculationInterval {
		e.lastHurstCalculationTime = currentTime
		hurst, err := indicator.CalculateHurstExponent(e.priceHistory, 2, 20)
		if err == nil {
			e.hurstExponent = hurst
			if hurst > 0.55 {
				e.currentRegime = RegimeTrending
			} else if hurst < 0.45 {
				e.currentRegime = RegimeMeanReverting
			} else {
				e.currentRegime = RegimeUnknown
			}
		}
	}

	// Update Volatility and dynamic OBI thresholds
	if e.config.DynamicOBIConf.Enabled {
		_, stdDev := e.volatilityCalc.Update(e.currentMidPrice)

		longAdjustment := e.config.DynamicOBIConf.VolatilityFactor * stdDev
		adjustedLongThreshold := e.config.LongOBIBaseThreshold * (1 + longAdjustment)
		minLong := e.config.LongOBIBaseThreshold * e.config.DynamicOBIConf.MinThresholdFactor
		maxLong := e.config.LongOBIBaseThreshold * e.config.DynamicOBIConf.MaxThresholdFactor
		e.currentLongOBIThreshold = math.Max(minLong, math.Min(adjustedLongThreshold, maxLong))

		shortAdjustment := e.config.DynamicOBIConf.VolatilityFactor * stdDev
		adjustedShortThreshold := e.config.ShortOBIBaseThreshold * (1 + shortAdjustment)
		minShort := e.config.ShortOBIBaseThreshold * e.config.DynamicOBIConf.MinThresholdFactor
		maxShort := e.config.ShortOBIBaseThreshold * e.config.DynamicOBIConf.MaxThresholdFactor
		e.currentShortOBIThreshold = math.Max(minShort, math.Min(adjustedShortThreshold, maxShort))
	} else {
		e.currentLongOBIThreshold = e.config.LongOBIBaseThreshold
		e.currentShortOBIThreshold = e.config.ShortOBIBaseThreshold
	}

	// Update OFI (T-13 related)
	// Convert float64 to decimal.Decimal for OFICalculator
	decBestBid := decimal.NewFromFloat(bestBid)
	decBestAsk := decimal.NewFromFloat(bestAsk)
	decBestBidSize := decimal.NewFromFloat(bestBidSize)
	decBestAskSize := decimal.NewFromFloat(bestAskSize)

	_ = e.ofiCalc.UpdateAndCalculateOFI( // Result can be stored or used if needed
		decBestBid, decBestAsk,
		decBestBidSize, decBestAskSize,
	)
}

// Evaluate evaluates the current OBI value against (potentially dynamic) thresholds
// and returns a TradingSignal if a new signal is confirmed, otherwise nil.
func (e *SignalEngine) Evaluate(currentTime time.Time, obiValue float64) *TradingSignal {
	// Update OBI history
	if e.slopeFilterConfig.Enabled {
		e.obiHistory = append(e.obiHistory, obiValue)
		if len(e.obiHistory) > e.slopeFilterConfig.Period {
			e.obiHistory = e.obiHistory[1:]
		}
	}

	longThreshold := e.currentLongOBIThreshold
	shortThreshold := e.currentShortOBIThreshold

	switch e.currentRegime {
	case RegimeTrending:
		longThreshold *= 0.9
		shortThreshold *= 0.9
	case RegimeMeanReverting:
		longThreshold *= 1.1
		shortThreshold *= 1.1
	}

	rawSignal := SignalNone
	if obiValue >= longThreshold {
		rawSignal = SignalLong
	} else if obiValue <= shortThreshold {
		rawSignal = SignalShort
	}

	// Apply OBI slope filter
	if e.slopeFilterConfig.Enabled && rawSignal != SignalNone {
		slope := e.calculateOBISlope()
		if rawSignal == SignalLong && slope < e.slopeFilterConfig.Threshold {
			rawSignal = SignalNone
		}
		if rawSignal == SignalShort && slope > -e.slopeFilterConfig.Threshold {
			rawSignal = SignalNone
		}
	}

	// Added for debugging
	// logger.Debugf("OBI: %.4f, LongThr: %.4f, ShortThr: %.4f, RawSignal: %s, CurrentSignal: %s",
	// 	obiValue, longThreshold, -shortThreshold, rawSignal, e.currentSignal)

	if rawSignal != e.currentSignal {
		if e.config.SignalHoldDuration > 0 {
			logger.Debugf("Signal changed from %s to %s. Resetting hold timer.", e.currentSignal, rawSignal)
			e.currentSignal = rawSignal
			e.currentSignalSince = currentTime
			if rawSignal == SignalNone {
				e.lastSignal = SignalNone
			}
			return nil // Not confirmed yet, or just ended.
		}
		// If hold duration is zero, we can proceed with the new signal immediately.
		e.currentSignal = rawSignal
		e.currentSignalSince = currentTime
	}

	if e.currentSignal == SignalNone {
		if e.lastSignal != SignalNone {
			e.lastSignal = SignalNone
		}
		return nil
	}

	holdDuration := currentTime.Sub(e.currentSignalSince)
	// logger.Debugf("Signal %s held for %v. Required: %v", e.currentSignal, holdDuration, e.config.SignalHoldDuration)

	if holdDuration >= e.config.SignalHoldDuration || e.config.SignalHoldDuration == 0 {
		if e.lastSignal != e.currentSignal && e.currentSignal != SignalNone {
			e.lastSignal = e.currentSignal
			e.lastSignalTime = currentTime

			logMessage := fmt.Sprintf("Signal %s confirmed. OBI: %.4f", e.currentSignal, obiValue)
			if e.config.SignalHoldDuration > 0 {
				logMessage += fmt.Sprintf(", Held for: %v", holdDuration)
			}
			logger.Debug(logMessage)


			// Calculate TP/SL prices based on currentMidPrice at signal confirmation
			var tpPrice, slPrice float64
			entryPrice := e.currentMidPrice // Use mid-price at signal confirmation as entry basis

			if e.currentSignal == SignalLong {
				tpPrice = entryPrice + e.longTP
				slPrice = entryPrice + e.longSL // SL is typically negative
			} else if e.currentSignal == SignalShort {
				tpPrice = entryPrice - e.shortTP
				slPrice = entryPrice - e.shortSL // SL is typically negative, so this becomes entry - (-value) = entry + value
			}

			return &TradingSignal{
				Type:               e.currentSignal,
				EntryPrice:         entryPrice,
				TakeProfit:         tpPrice,
				StopLoss:           slPrice,
				TriggerOBIOBIValue: obiValue,
				TriggerTime:        currentTime,
			}
		}
		// logger.Debugf("Signal %s already triggered. Ignoring.", e.currentSignal)
		return nil // Already reported or no new signal state
	}
	// logger.Debugf("Signal %s not held long enough. Current hold: %v, Required: %v", e.currentSignal, holdDuration, e.config.SignalHoldDuration)
	return nil // Active and stable, but not yet persisted long enough.
}

// GetCurrentLongOBIThreshold returns the current (potentially dynamic) OBI threshold for long signals.
func (e *SignalEngine) GetCurrentLongOBIThreshold() float64 {
	return e.currentLongOBIThreshold
}

// GetCurrentShortOBIThreshold returns the current (potentially dynamic) OBI threshold for short signals.
func (e *SignalEngine) GetCurrentShortOBIThreshold() float64 {
	return e.currentShortOBIThreshold
}

// calculateOBISlope calculates the slope of the OBI history using linear regression.
func (e *SignalEngine) calculateOBISlope() float64 {
	n := len(e.obiHistory)
	if n < e.slopeFilterConfig.Period {
		return 0.0 // Not enough data
	}

	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range e.obiHistory {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	floatN := float64(n)
	denominator := floatN*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0.0 // Avoid division by zero
	}

	slope := (floatN*sumXY - sumX*sumY) / denominator
	return slope
}

// Helper function (can be moved to a common utility if used elsewhere)
// Consider precision if converting float64 to string for decimal.
// For now, this is a placeholder. A more robust conversion might be needed.
// This float64ToString is not defined in indicator pkg. It's a conceptual placeholder.
// Let's assume direct decimal.NewFromFloat for now, and if precision issues arise, address them.
// For OFI, direct NewFromFloat should be fine if input floats are already clean.
// The OFICalculator was designed with decimal.Decimal.
// Let's remove the placeholder `indicator.Float64ToString` and use `decimal.NewFromFloat` directly.
// The `UpdateMarketData` will use `decimal.NewFromFloat` for OFI parameters.

// The `indicator.Float64ToString` was a misinterpretation.
// `decimal.NewFromFloat(value)` is the correct way.
// Re-adjusting the UpdateMarketData for OFI part.
// The previous change to UpdateMarketData already used decimal.NewFromFloat indirectly via indicator.Float64ToString.
// Let's ensure it's clean.
// The OFI calculator expects decimal.Decimal. The inputs `bestBid`, etc. are float64.
// So, conversion is `dBestBid := decimal.NewFromFloat(bestBid)`
// This has been done in the diff by using `decimal.NewFromString(indicator.Float64ToString(bestBid))`
// which is more complex than `decimal.NewFromFloat(bestBid)`.
// Let's simplify that part of the diff.

// Corrected OFI part in UpdateMarketData in the diff that will be generated:
//	decBestBid := decimal.NewFromFloat(bestBid)
//	decBestAsk := decimal.NewFromFloat(bestAsk)
//	decBestBidSize := decimal.NewFromFloat(bestBidSize)
//	decBestAskSize := decimal.NewFromFloat(bestAskSize)
//
//	_ = e.ofiCalc.UpdateAndCalculateOFI(
//		decBestBid, decBestAsk,
//		decBestBidSize, decBestAskSize,
//	)
// This correction will be applied in the actual diff generation.
// The current diff uses `NewFromString(indicator.Float64ToString(floatVal))`.
// I will fix this in the actual application of the change.
// The `replace_with_git_merge_diff` tool will receive the corrected version.
// The `indicator.Float64ToString` helper does not exist, so it must be `decimal.NewFromFloat`.
// The provided diff has this structure:
// decBestBid, _ := decimal.NewFromString(indicator.Float64ToString(bestBid))
// This needs to be changed to:
// decBestBid := decimal.NewFromFloat(bestBid)
// (and similarly for other decimal conversions for OFI).
// The `shopspring/decimal` package should be imported.
// The provided diff already imports `shopspring/decimal`.
// The tool will apply the diff as is for now, and if there's a compile error due to `indicator.Float64ToString`
// I will fix it in the next step. For now, I'll assume it's a placeholder that was intended to be `decimal.NewFromFloat`.

// The provided diff is:
// +	"github.com/shopspring/decimal"
// ...
// +	decBestBid, _ := decimal.NewFromString(indicator.Float64ToString(bestBid))
// This `indicator.Float64ToString` is the issue. I will proceed with this diff and fix this specific conversion
// in a subsequent step if the linter/compiler complains, or ideally, preemptively fix it if I can modify the diff content directly.
// Since I cannot modify the diff content directly before the tool call, I will make a note to fix this after this step.
// For now, I will assume the tool applies the diff and I'll address `indicator.Float64ToString` immediately after.

// The user instruction implies that Jules should make a best effort to ensure CI passes.
// So, I should fix the `indicator.Float64ToString` part.
// The `replace_with_git_merge_diff` tool takes the code block as is.
// I will modify the SEARCH/REPLACE block to use `decimal.NewFromFloat()`

// Corrected version for the diff:
// Replace:
// +	decBestBid, _ := decimal.NewFromString(indicator.Float64ToString(bestBid))
// +	decBestAsk, _ := decimal.NewFromString(indicator.Float64ToString(bestAsk))
// +	decBestBidSize, _ := decimal.NewFromString(indicator.Float64ToString(bestBidSize))
// +	decBestAskSize, _ := decimal.NewFromString(indicator.Float64ToString(bestAskSize))
// With:
// +	decBestBid := decimal.NewFromFloat(bestBid)
// +	decBestAsk := decimal.NewFromFloat(bestAsk)
// +	decBestBidSize := decimal.NewFromFloat(bestBidSize)
// +	decBestAskSize := decimal.NewFromFloat(bestAskSize)
// This change will be incorporated into the diff provided to the tool.
