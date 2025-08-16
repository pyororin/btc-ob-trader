package signal

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/internal/datastore"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
	"github.com/your-org/obi-scalp-bot/pkg/cvd"
)

func TestSignalEngine_Evaluate_WithFixture(t *testing.T) {
	// This test uses a real data fixture to test the signal engine's behavior
	// over a sequence of market events.

	weights := map[string]float64{
		"obi":        0.6,
		"ofi":        0.1,
		"cvd":        0.2,
		"microprice": 0.1,
	}
	// Use a low threshold to ensure signals are generated from the fixture data
	engine := newTestSignalEngine(100, 0.2, weights)

	// Path to the test data - relative to the signal package
	csvPath, err := filepath.Abs("../datastore/testdata/order_book_updates_20250803-140150.csv")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, errCh := datastore.StreamMarketEventsFromCSV(ctx, csvPath)

	orderBook := indicator.NewOrderBook()
	var signalsGenerated int

	for {
		select {
		case marketEvent, ok := <-eventCh:
			if !ok {
				goto EndLoop
			}

			var obiResult indicator.OBIResult
			var bestBidSize, bestAskSize float64

			switch event := marketEvent.(type) {
			case datastore.OrderBookEvent:
				orderBook.ApplyUpdate(event.OrderBookData)
				res, valid := orderBook.CalculateOBI(indicator.OBILevels...)
				if !valid {
					continue
				}
				obiResult = res
				bestBidSize, bestAskSize = orderBook.GetBestBidAskSize()
			case datastore.TradeEvent:
				// Trades don't update the book directly in this stream,
				// but we can use the last known OBI result.
			}

			if obiResult.BestBid <= 0 || obiResult.BestAsk <= 0 {
				continue
			}

			midPrice := (obiResult.BestAsk + obiResult.BestBid) / 2
			engine.UpdateMarketData(marketEvent.GetTime(), midPrice, obiResult.BestBid, obiResult.BestAsk, bestBidSize, bestAskSize, nil)

			if signal := engine.Evaluate(marketEvent.GetTime(), obiResult.OBI8); signal != nil {
				signalsGenerated++
				t.Logf("Generated signal: %s at %s (OBI: %.4f)", signal.Type, marketEvent.GetTime(), obiResult.OBI8)
			}

		case err := <-errCh:
			if err != nil {
				t.Fatalf("Error streaming market events: %v", err)
			}
			goto EndLoop
		case <-ctx.Done():
			goto EndLoop
		}
	}
EndLoop:

	// Assert that at least one signal was generated during the run.
	// This is a basic sanity check. More specific assertions could be added
	// if the expected behavior on the fixture data was known precisely.
	assert.Greater(t, signalsGenerated, 0, "Expected at least one signal to be generated from the fixture data")
	t.Logf("Finished processing fixture. Total signals generated: %d", signalsGenerated)
}

func newTestSignalEngine(holdDurationMs int, compositeThreshold float64, weights map[string]float64) *SignalEngine {
	tradeCfg := &config.TradeConfig{
		Long: config.StrategyConf{
			TP: 0.1,
			SL: -0.1,
		},
		Short: config.StrategyConf{
			TP: 0.1,
			SL: -0.1,
		},
		Signal: config.SignalConfig{
			HoldDurationMs:     holdDurationMs,
			CVDWindowMinutes:   1,
			CompositeThreshold: compositeThreshold,
			OBIWeight:          weights["obi"],
			OFIWeight:          weights["ofi"],
			CVDWeight:          weights["cvd"],
			MicroPriceWeight:   weights["microprice"],
		},
		Volatility: config.VolConf{
			EWMALambda: 0.5,
		},
	}
	engine, err := NewSignalEngine(tradeCfg)
	if err != nil {
		panic(err)
	}
	return engine
}

func TestSignalEngine_Evaluate_CompositeSignal(t *testing.T) {
	weights := map[string]float64{
		"obi":        0.5,
		"ofi":        0.2,
		"cvd":        0.2,
		"microprice": 0.1,
	}
	engine := newTestSignalEngine(100, 0.5, weights)

	// No signal
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil) // mid: 100, micro: 100
	engine.ofiValue = 0.1
	engine.cvdValue = 0.1
	signal := engine.Evaluate(time.Now(), 0.1) // score: 0.05 + 0.02 + 0.02 + 0 = 0.09 < 0.5
	assert.Nil(t, signal)

	// Long signal
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 1.0
	engine.cvdValue = 1.0
	signal = engine.Evaluate(time.Now(), 0.8) // score: 0.4 + 0.2 + 0.2 + 0 = 0.8 > 0.5
	assert.Nil(t, signal, "Should not signal immediately")

	time.Sleep(150 * time.Millisecond)
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 1.0
	engine.cvdValue = 1.0
	signal = engine.Evaluate(time.Now(), 0.8)
	if assert.NotNil(t, signal) {
		assert.Equal(t, SignalLong, signal.Type)
	}

	// Short signal
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = -1.0
	engine.cvdValue = -1.0
	signal = engine.Evaluate(time.Now(), -0.8) // score: -0.4 - 0.2 - 0.2 - 0 = -0.8 < -0.5
	assert.Nil(t, signal, "Should not signal immediately")

	time.Sleep(150 * time.Millisecond)
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = -1.0
	engine.cvdValue = -1.0
	signal = engine.Evaluate(time.Now(), -0.8)
	if assert.NotNil(t, signal) {
		assert.Equal(t, SignalShort, signal.Type)
	}
}

func TestSignalEngine_DynamicOBIThreshold(t *testing.T) {
	weights := map[string]float64{"obi": 1.0, "ofi": 0.0, "cvd": 0.0, "microprice": 0.0}
	engine := newTestSignalEngine(0, 0.1, weights)

	// Setup Dynamic OBI config
	engine.config.DynamicOBIConf = config.DynamicOBIConf{
		Enabled:          config.FlexBool(true),
		VolatilityFactor: 0.5,
		MinThresholdFactor: 0.8, // 80% of base
		MaxThresholdFactor: 1.5, // 150% of base
	}

	baseLongThreshold := 0.1
	baseShortThreshold := -0.1

	t.Run("dynamic OBI disabled", func(t *testing.T) {
		engine.config.DynamicOBIConf.Enabled = false
		defer func() { engine.config.DynamicOBIConf.Enabled = true }()
		assert.Equal(t, baseLongThreshold, engine.GetCurrentLongOBIThreshold())
		assert.Equal(t, baseShortThreshold, engine.GetCurrentShortOBIThreshold())
	})

	t.Run("volatility is zero", func(t *testing.T) {
		engine.volatilityCalc.Update(100) // First update, vol is 0
		assert.Equal(t, baseLongThreshold, engine.GetCurrentLongOBIThreshold(), "with zero volatility, threshold should be the base value")
		assert.Equal(t, baseShortThreshold, engine.GetCurrentShortOBIThreshold(), "with zero volatility, threshold should be the base value")
	})

	t.Run("volatility is positive", func(t *testing.T) {
		// Simulate some price movement to generate volatility
		engine.volatilityCalc.Update(100)
		engine.volatilityCalc.Update(101)
		engine.volatilityCalc.Update(102)
		vol := engine.volatilityCalc.GetEWMStandardDeviation()
		require.Greater(t, vol, 0.0)

		expectedLong := baseLongThreshold + (vol * engine.config.DynamicOBIConf.VolatilityFactor)
		expectedShort := baseShortThreshold - (vol * engine.config.DynamicOBIConf.VolatilityFactor)

		assert.InDelta(t, expectedLong, engine.GetCurrentLongOBIThreshold(), 1e-9)
		assert.InDelta(t, expectedShort, engine.GetCurrentShortOBIThreshold(), 1e-9)
	})

	t.Run("clamped by min threshold", func(t *testing.T) {
		// Set a negative volatility factor to test the lower bound
		engine.config.DynamicOBIConf.VolatilityFactor = -10.0
		defer func() { engine.config.DynamicOBIConf.VolatilityFactor = 0.5 }()

		engine.volatilityCalc.Update(100)
		engine.volatilityCalc.Update(101)
		vol := engine.volatilityCalc.GetEWMStandardDeviation()
		require.Greater(t, vol, 0.0)

		minLong := baseLongThreshold * engine.config.DynamicOBIConf.MinThresholdFactor
		maxShort := baseShortThreshold * engine.config.DynamicOBIConf.MinThresholdFactor // e.g., -0.1 * 0.8 = -0.08

		assert.Equal(t, minLong, engine.GetCurrentLongOBIThreshold(), "long threshold should be clamped at its minimum")
		assert.Equal(t, maxShort, engine.GetCurrentShortOBIThreshold(), "short threshold should be clamped at its maximum (closest to zero)")
	})

	t.Run("clamped by max threshold", func(t *testing.T) {
		engine.config.DynamicOBIConf.VolatilityFactor = 10.0
		defer func() { engine.config.DynamicOBIConf.VolatilityFactor = 0.5 }()

		engine.volatilityCalc.Update(100)
		engine.volatilityCalc.Update(101)
		vol := engine.volatilityCalc.GetEWMStandardDeviation()
		require.Greater(t, vol, 0.0)

		maxLong := baseLongThreshold * engine.config.DynamicOBIConf.MaxThresholdFactor
		minShort := baseShortThreshold * engine.config.DynamicOBIConf.MaxThresholdFactor // e.g., -0.1 * 1.5 = -0.15

		assert.Equal(t, maxLong, engine.GetCurrentLongOBIThreshold(), "long threshold should be clamped at its maximum")
		assert.Equal(t, minShort, engine.GetCurrentShortOBIThreshold(), "short threshold should be clamped at its minimum (furthest from zero)")
	})
}

func TestSignalEngine_CVDAndTimeWindow(t *testing.T) {
	weights := map[string]float64{
		"obi":        0.4,
		"ofi":        0.1,
		"cvd":        0.5, // Give CVD a high weight for this test
		"microprice": 0.0,
	}
	// Set a short hold duration for quicker testing, and a 1-minute CVD window
	engine := newTestSignalEngine(50, 0.5, weights)
	engine.config.CVDWindow = 1 * time.Minute

	// --- Initial State ---
	baseTime := time.Now()
	assert.Equal(t, 0.0, engine.cvdValue, "Initial CVD should be zero")

	// --- Step 1: A buy trade occurs, increasing CVD ---
	buyTrade1 := cvd.Trade{ID: "1", Side: "buy", Price: 100, Size: 2.0, Timestamp: baseTime}
	// OBI and OFI are neutral, signal should be driven by CVD
	engine.UpdateMarketData(baseTime, 100, 99.9, 100.1, 10, 10, []cvd.Trade{buyTrade1})
	engine.ofiValue = 0.0
	// Expected composite score: (0.0 * 0.4) + (0.0 * 0.1) + (2.0 * 0.5) = 1.0
	signal := engine.Evaluate(baseTime, 0.0)
	assert.Nil(t, signal, "Signal should not be generated immediately")
	assert.Equal(t, SignalLong, engine.currentSignal, "Raw signal should be Long")
	assert.Equal(t, 2.0, engine.cvdValue, "CVD should be 2.0 after one buy trade")

	// --- Step 2: Hold duration passes, signal is confirmed ---
	time.Sleep(60 * time.Millisecond)
	currentTime := baseTime.Add(60 * time.Millisecond)
	engine.UpdateMarketData(currentTime, 100, 99.9, 100.1, 10, 10, nil) // No new trades
	engine.ofiValue = 0.0
	signal = engine.Evaluate(currentTime, 0.0)
	if assert.NotNil(t, signal, "A long signal should be confirmed after hold duration") {
		assert.Equal(t, SignalLong, signal.Type)
	}
	assert.Equal(t, 2.0, engine.cvdValue, "CVD should persist")

	// --- Step 3: Time passes, but still within the CVD window ---
	currentTime = baseTime.Add(30 * time.Second)
	engine.UpdateMarketData(currentTime, 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 0.0
	signal = engine.Evaluate(currentTime, 0.0)
	assert.Nil(t, signal, "Signal should not be re-triggered")
	assert.Equal(t, 2.0, engine.cvdValue, "CVD should still be 2.0 within the window")

	// --- Step 4: Time exceeds the CVD window, the initial trade expires ---
	currentTime = baseTime.Add(70 * time.Second) // 1 minute 10 seconds later
	engine.UpdateMarketData(currentTime, 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 0.0
	// Now that the trade from t1 is outside the 1-minute window, CVD should be 0
	// Expected composite score: (0.0 * 0.4) + (0.0 * 0.1) + (0.0 * 0.5) = 0.0
	signal = engine.Evaluate(currentTime, 0.0)
	assert.Nil(t, signal, "Signal should be None as CVD has decayed")
	assert.Equal(t, SignalNone, engine.currentSignal, "Current signal should revert to None")
	assert.Equal(t, 0.0, engine.cvdValue, "CVD should decay to zero after window passes")
}

func TestSignalEngine_SpreadFilter(t *testing.T) {
	weights := map[string]float64{"obi": 1.0, "ofi": 0.0, "cvd": 0.0, "microprice": 0.0}
	engine := newTestSignalEngine(0, 0.1, weights)

	// Set a spread limit of 50
	engine.config.SpreadLimit = 50.0

	// --- Scenario 1: Spread is wider than the limit ---
	// Set a strong OBI, but a wide spread
	bestBid := 5000000.0
	bestAsk := 5000100.0 // Spread is 100, which is > 50
	obiValue := 0.8       // Strong long signal

	engine.UpdateMarketData(time.Now(), (bestBid+bestAsk)/2, bestBid, bestAsk, 1.0, 1.0, nil)
	signal := engine.Evaluate(time.Now(), obiValue)

	assert.Nil(t, signal, "Signal should be nil because spread is too wide")

	// --- Scenario 2: Spread is narrower than the limit ---
	// Set a strong OBI with a narrow spread
	bestBid = 5000000.0
	bestAsk = 5000020.0 // Spread is 20, which is < 50
	obiValue = 0.8      // Strong long signal

	engine.UpdateMarketData(time.Now(), (bestBid+bestAsk)/2, bestBid, bestAsk, 1.0, 1.0, nil)
	signal = engine.Evaluate(time.Now(), obiValue) // First call sets the state
	assert.Nil(t, signal, "First call with valid spread should set state and return nil")
	assert.Equal(t, SignalLong, engine.currentSignal)

	signal = engine.Evaluate(time.Now(), obiValue) // Second call confirms the signal
	if assert.NotNil(t, signal, "Second call should generate a signal because spread is within limit") {
		assert.Equal(t, SignalLong, signal.Type)
	}

	// --- Scenario 3: Spread limit is zero (disabled) ---
	engine.config.SpreadLimit = 0.0
	engine.currentSignal = SignalNone // Reset state
	engine.lastSignal = SignalNone    // Reset state
	bestBid = 5000000.0
	bestAsk = 5000100.0 // Spread is 100, but limit is disabled
	obiValue = 0.8      // Strong long signal

	engine.UpdateMarketData(time.Now(), (bestBid+bestAsk)/2, bestBid, bestAsk, 1.0, 1.0, nil)
	signal = engine.Evaluate(time.Now(), obiValue) // First call
	assert.Nil(t, signal, "First call with disabled limit should set state and return nil")
	assert.Equal(t, SignalLong, engine.currentSignal)

	signal = engine.Evaluate(time.Now(), obiValue) // Second call
	if assert.NotNil(t, signal, "Second call should generate a signal because spread limit is disabled") {
		assert.Equal(t, SignalLong, signal.Type)
	}
}
