// Package signal provides the logic for generating trading signals.
package signal

import (
	"math" // Required for math.Min/Max
	"time"

	"github.com/shopspring/decimal"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/indicator" // Added
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

	// Fields for regime detection
	priceHistory []float64
	maxHistory   int
	currentRegime Regime
	hurstExponent float64
}

// NewSignalEngine creates a new SignalEngine.
func NewSignalEngine(cfg *config.Config) (*SignalEngine, error) {
	signalHoldDuration := time.Duration(cfg.Signal.HoldDurationMs) * time.Millisecond

	engineCfg := EngineConfig{
		LongOBIBaseThreshold:  cfg.Long.OBIThreshold,
		ShortOBIBaseThreshold: cfg.Short.OBIThreshold,
		SignalHoldDuration:    signalHoldDuration,
		DynamicOBIConf:        cfg.Volatility.DynamicOBI, // Added
	}

	return &SignalEngine{
		config:                   engineCfg,
		volatilityCalc:           indicator.NewVolatilityCalculator(cfg.Volatility.EWMALambda), // Added
		ofiCalc:                  indicator.NewOFICalculator(),                               // Added
		lastSignal:               SignalNone,
		currentSignal:            SignalNone,
		currentLongOBIThreshold:  engineCfg.LongOBIBaseThreshold,  // Initialize with base
		currentShortOBIThreshold: engineCfg.ShortOBIBaseThreshold, // Initialize with base
		longTP:                   cfg.Long.TP,
		longSL:                   cfg.Long.SL,
		shortTP:                  cfg.Short.TP,
		shortSL:                  cfg.Short.SL,

		// Initialize regime detection fields
		priceHistory: make([]float64, 0, 100),
		maxHistory:   100,
		currentRegime: RegimeUnknown,
		hurstExponent: 0.0,
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

	// Update regime
	if len(e.priceHistory) == e.maxHistory {
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
	} else if obiValue <= -shortThreshold {
		rawSignal = SignalShort
	}

	if rawSignal != e.currentSignal {
		e.currentSignal = rawSignal
		e.currentSignalSince = currentTime
		if rawSignal == SignalNone {
			e.lastSignal = SignalNone
		}
		return nil // Not confirmed yet, or just ended.
	}

	if e.currentSignal == SignalNone {
		if e.lastSignal != SignalNone {
			e.lastSignal = SignalNone
		}
		return nil
	}

	if currentTime.Sub(e.currentSignalSince) >= e.config.SignalHoldDuration {
		if e.lastSignal != e.currentSignal && e.currentSignal != SignalNone {
			e.lastSignal = e.currentSignal
			e.lastSignalTime = currentTime

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
				Type:        e.currentSignal,
				EntryPrice:  entryPrice,
				TakeProfit:  tpPrice,
				StopLoss:    slPrice,
				TriggerOBIOBIValue: obiValue,
				TriggerTime: currentTime,
			}
		}
		return nil // Already reported or no new signal state
	}
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
