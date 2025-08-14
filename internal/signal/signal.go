// Package signal provides the logic for generating trading signals.
package signal

import (
	"time"

	"github.com/shopspring/decimal"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
	"github.com/your-org/obi-scalp-bot/pkg/cvd"
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
	SignalHoldDuration    time.Duration
	EntryPriceOffset      float64
	DynamicOBIConf        config.DynamicOBIConf
	CVDWindow             time.Duration
	OBIWeight             float64
	OFIWeight             float64
	CVDWeight             float64
	MicroPriceWeight      float64
	CompositeThreshold    float64
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
	cvdCalc                  *cvd.CVDCalculator
	lastSignal               SignalType
	lastSignalTime           time.Time
	currentSignal            SignalType
	currentSignalSince       time.Time
	currentMidPrice          float64
	bestBid                  float64
	bestAsk                  float64
	longTP                   float64
	longSL                   float64
	shortTP                  float64
	shortSL                  float64
	ofiValue                 float64
	cvdValue                 float64
	microPrice               float64

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
	cvdWindow := time.Duration(tradeCfg.Signal.CVDWindowMinutes) * time.Minute

	engineCfg := EngineConfig{
		SignalHoldDuration:    signalHoldDuration,
		EntryPriceOffset:      tradeCfg.EntryPriceOffset,
		DynamicOBIConf:        tradeCfg.Volatility.DynamicOBI,
		CVDWindow:             cvdWindow,
		OBIWeight:             tradeCfg.Signal.OBIWeight,
		OFIWeight:             tradeCfg.Signal.OFIWeight,
		CVDWeight:             tradeCfg.Signal.CVDWeight,
		MicroPriceWeight:      tradeCfg.Signal.MicroPriceWeight,
		CompositeThreshold:    tradeCfg.Signal.CompositeThreshold,
	}

	logger.Debugf("[SignalEngine] Initialized with weights: OBI=%.2f, OFI=%.2f, CVD=%.2f, MicroPrice=%.2f, Threshold=%.2f",
		engineCfg.OBIWeight,
		engineCfg.OFIWeight,
		engineCfg.CVDWeight,
		engineCfg.MicroPriceWeight,
		engineCfg.CompositeThreshold,
	)

	return &SignalEngine{
		config:                   engineCfg,
		volatilityCalc:           indicator.NewVolatilityCalculator(tradeCfg.Volatility.EWMALambda),
		ofiCalc:                  indicator.NewOFICalculator(),
		cvdCalc:                  cvd.NewCVDCalculator(cvdWindow),
		lastSignal:               SignalNone,
		currentSignal:            SignalNone,
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

// UpdateMarketData updates the engine with the latest market data.
func (e *SignalEngine) UpdateMarketData(currentTime time.Time, currentMidPrice, bestBid, bestAsk, bestBidSize, bestAskSize float64, trades []cvd.Trade) {
	e.currentMidPrice = currentMidPrice
	e.bestBid = bestBid
	e.bestAsk = bestAsk

	// Update price history for regime detection
	e.priceHistory = append(e.priceHistory, currentMidPrice)
	if len(e.priceHistory) > e.maxHistory {
		e.priceHistory = e.priceHistory[1:]
	}

	// Update regime periodically
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

	// Update Volatility
	e.volatilityCalc.Update(e.currentMidPrice)

	// Update indicators
	e.microPrice = indicator.CalculateMicroPrice(bestBid, bestAsk, bestBidSize, bestAskSize)
	// e.cvdValue = e.cvdCalc.Update(trades, currentTime) // Temporarily commented out for debugging

	decBestBid := decimal.NewFromFloat(bestBid)
	decBestAsk := decimal.NewFromFloat(bestAsk)
	decBestBidSize := decimal.NewFromFloat(bestBidSize)
	decBestAskSize := decimal.NewFromFloat(bestAskSize)
	ofiDecimal := e.ofiCalc.UpdateAndCalculateOFI(decBestBid, decBestAsk, decBestBidSize, decBestAskSize)
	e.ofiValue, _ = ofiDecimal.Float64()
}

// Evaluate evaluates the current market data and returns a TradingSignal if a new signal is confirmed.
func (e *SignalEngine) Evaluate(currentTime time.Time, obiValue float64) *TradingSignal {
	// --- 1. Calculate Composite Score ---
	microPriceDiff := e.microPrice - e.currentMidPrice
	obiComponent := obiValue * e.config.OBIWeight
	ofiComponent := e.ofiValue * e.config.OFIWeight
	cvdComponent := e.cvdValue * e.config.CVDWeight
	microPriceComponent := 0.0
	if e.config.MicroPriceWeight > 0 {
		microPriceComponent = microPriceDiff * e.config.MicroPriceWeight
	}
	compositeScore := obiComponent + ofiComponent + cvdComponent + microPriceComponent

	longThreshold := e.GetCurrentLongOBIThreshold()
	shortThreshold := e.GetCurrentShortOBIThreshold()

	logger.Debugf(
		"[SignalCalc] Composite Score: %.4f (LongThr: %.4f, ShortThr: %.4f) | OBI: %.4f (Val: %.4f, W: %.2f) | OFI: %.4f (Val: %.4f, W: %.2f) | CVD: %.4f (Val: %.4f, W: %.2f) | MicroPrice: %.4f (Diff: %.4f, W: %.2f)",
		compositeScore, longThreshold, shortThreshold,
		obiComponent, obiValue, e.config.OBIWeight,
		ofiComponent, e.ofiValue, e.config.OFIWeight,
		cvdComponent, e.cvdValue, e.config.CVDWeight,
		microPriceComponent, microPriceDiff, e.config.MicroPriceWeight,
	)

	// --- 2. Determine Raw Signal from Score ---
	rawSignal := SignalNone
	if compositeScore >= longThreshold {
		rawSignal = SignalLong
	} else if compositeScore <= shortThreshold {
		rawSignal = SignalShort
	}

	// --- 3. Apply Filters ---
	// Update score history for slope filter
	if bool(e.slopeFilterConfig.Enabled) {
		e.obiHistory = append(e.obiHistory, obiValue)
		if len(e.obiHistory) > e.slopeFilterConfig.Period {
			e.obiHistory = e.obiHistory[1:]
		}

		// Apply slope filter
		if rawSignal != SignalNone {
			slope := e.calculateOBISlope()
			if rawSignal == SignalLong && slope < e.slopeFilterConfig.Threshold {
				logger.Debugf("[SignalFilter] Slope filter triggered for LONG signal. Slope: %.4f, Threshold: %.4f. Discarding signal.", slope, e.slopeFilterConfig.Threshold)
				rawSignal = SignalNone
			}
			if rawSignal == SignalShort && slope > -e.slopeFilterConfig.Threshold {
				logger.Debugf("[SignalFilter] Slope filter triggered for SHORT signal. Slope: %.4f, Threshold: %.4f. Discarding signal.", slope, -e.slopeFilterConfig.Threshold)
				rawSignal = SignalNone
			}
		}
	}

	// --- 4. Manage Signal State and Hold Duration ---
	if rawSignal != e.currentSignal {
		logger.Debugf("[SignalState] State changed from %s to %s. Resetting hold timer.", e.currentSignal, rawSignal)
		e.currentSignal = rawSignal
		e.currentSignalSince = currentTime
		if rawSignal == SignalNone {
			e.lastSignal = SignalNone
		}
		return nil // State changed, requires confirmation or is now neutral.
	}

	// If there's no active signal, nothing to do.
	if e.currentSignal == SignalNone {
		return nil
	}

	// If we have an active signal, check if it's already been triggered.
	if e.lastSignal == e.currentSignal {
		logger.Debugf("[SignalState] Signal %s already triggered and active. Ignoring.", e.currentSignal)
		return nil
	}

	// --- 5. Confirm Signal based on Hold Duration or Stability ---
	holdDuration := currentTime.Sub(e.currentSignalSince)

	// A "stable" signal (well beyond the threshold) can be confirmed faster.
	isStableSignal := false
	stableThresholdFactor := 1.5
	if e.currentSignal == SignalLong && compositeScore >= longThreshold*stableThresholdFactor {
		isStableSignal = true
	} else if e.currentSignal == SignalShort && compositeScore <= shortThreshold*stableThresholdFactor {
		isStableSignal = true
	}

	isHoldDurationMet := e.config.SignalHoldDuration == 0 || holdDuration >= e.config.SignalHoldDuration
	if isHoldDurationMet || isStableSignal {
		e.lastSignal = e.currentSignal
		e.lastSignalTime = currentTime

		reason := "hold duration met"
		if isStableSignal {
			reason = "signal is stable"
		} else if e.config.SignalHoldDuration == 0 {
			reason = "hold duration is zero"
		}

		logger.Infof("[SignalConfirm] Confirmed %s signal (Reason: %s). Score: %.4f, OBI: %.4f, Hold: %v", e.currentSignal, reason, compositeScore, obiValue, holdDuration)

		// Calculate TP/SL prices based on currentMidPrice at signal confirmation
		var tpPrice, slPrice, entryPrice float64

		if e.currentSignal == SignalLong {
			entryPrice = e.bestAsk + e.config.EntryPriceOffset
			tpPrice = entryPrice + e.longTP
			slPrice = entryPrice + e.longSL // SL is typically negative
		} else if e.currentSignal == SignalShort {
			entryPrice = e.bestBid - e.config.EntryPriceOffset
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

	logger.Debugf("[SignalState] Signal %s not held long enough. Current hold: %v, Required: %v", e.currentSignal, holdDuration, e.config.SignalHoldDuration)
	return nil // Signal is active but not yet confirmed.
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

// GetCurrentLongOBIThreshold returns the current dynamic threshold for a long signal.
func (e *SignalEngine) GetCurrentLongOBIThreshold() float64 {
	baseThreshold := e.config.CompositeThreshold
	if !bool(e.config.DynamicOBIConf.Enabled) {
		return baseThreshold
	}

	volatility := e.volatilityCalc.GetEWMStandardDeviation()
	dynamicThreshold := baseThreshold + (volatility * e.config.DynamicOBIConf.VolatilityFactor)

	minThreshold := baseThreshold * e.config.DynamicOBIConf.MinThresholdFactor
	if dynamicThreshold < minThreshold {
		dynamicThreshold = minThreshold
	}

	maxThreshold := baseThreshold * e.config.DynamicOBIConf.MaxThresholdFactor
	if dynamicThreshold > maxThreshold {
		dynamicThreshold = maxThreshold
	}

	return dynamicThreshold
}

// GetCurrentShortOBIThreshold returns the current dynamic threshold for a short signal.
func (e *SignalEngine) GetCurrentShortOBIThreshold() float64 {
	// For short signals, the threshold is negative.
	baseThreshold := -e.config.CompositeThreshold
	if !bool(e.config.DynamicOBIConf.Enabled) {
		return baseThreshold
	}

	// Volatility should make the threshold further from zero (i.e., more negative).
	volatility := e.volatilityCalc.GetEWMStandardDeviation()
	dynamicThreshold := baseThreshold - (volatility * e.config.DynamicOBIConf.VolatilityFactor)

	// Clamp the threshold. Note that min/max logic is inverted for negative numbers.
	minThreshold := baseThreshold * e.config.DynamicOBIConf.MaxThresholdFactor // e.g., -0.1 * 1.5 = -0.15 (more negative)
	if dynamicThreshold < minThreshold {
		dynamicThreshold = minThreshold
	}

	maxThreshold := baseThreshold * e.config.DynamicOBIConf.MinThresholdFactor // e.g., -0.1 * 0.8 = -0.08 (less negative)
	if dynamicThreshold > maxThreshold {
		dynamicThreshold = maxThreshold
	}

	return dynamicThreshold
}
