package signal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/your-org/obi-scalp-bot/internal/config"
	"github.com/your-org/obi-scalp-bot/pkg/cvd"
)

func newTestSignalEngine(holdDurationMs int, compositeThreshold float64, weights map[string]float64) *SignalEngine {
	tradeCfg := &config.TradeConfig{
		Long: config.StrategyConf{
			OBIThreshold: 0.5,
			TP:           0.1,
			SL:           -0.1,
		},
		Short: config.StrategyConf{
			OBIThreshold: -0.5,
			TP:           0.1,
			SL:           -0.1,
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

func TestSignalHoldDuration(t *testing.T) {
	weights := map[string]float64{"obi": 1.0, "ofi": 0.0, "cvd": 0.0, "microprice": 0.0}
	holdDurationMs := 500
	baseTime := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)

	// --- Scenario A: Signal holds long enough and is confirmed ---
	t.Run("Scenario A: Signal Confirmed", func(t *testing.T) {
		// Reset engine state
		engine := newTestSignalEngine(holdDurationMs, 0.5, weights)

		// 1. Signal appears
		currentTime := baseTime
		signal := engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "A_1: Signal should not be confirmed immediately")
		assert.Equal(t, SignalLong, engine.currentSignal, "A_1: Raw signal should be LONG")
		assert.Equal(t, currentTime, engine.currentSignalSince, "A_1: Signal since time should be set")

		// 2. Time advances, but not enough to confirm
		currentTime = baseTime.Add(300 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "A_2: Signal should not be confirmed after 300ms")
		assert.Equal(t, SignalLong, engine.currentSignal, "A_2: Raw signal should still be LONG")

		// 3. Time advances past the hold duration
		currentTime = baseTime.Add(501 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.6)
		if assert.NotNil(t, signal, "A_3: Signal should be confirmed after 501ms") {
			assert.Equal(t, SignalLong, signal.Type)
		}

		// 4. Signal should not be re-triggered immediately after confirmation
		currentTime = baseTime.Add(502 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "A_4: Signal should not be re-triggered")
	})

	// --- Scenario B: Signal disappears before hold duration is met ---
	t.Run("Scenario B: Signal Disappears", func(t *testing.T) {
		// Reset engine state
		engine := newTestSignalEngine(holdDurationMs, 0.5, weights)

		// 1. Signal appears
		currentTime := baseTime
		signal := engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "B_1: Signal should not be confirmed immediately")
		assert.Equal(t, SignalLong, engine.currentSignal, "B_1: Raw signal should be LONG")

		// 2. Time advances, but signal disappears (OBI drops)
		currentTime = baseTime.Add(300 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.4) // OBI drops below threshold
		assert.Nil(t, signal, "B_2: Signal should be nil as it disappeared")
		assert.Equal(t, SignalNone, engine.currentSignal, "B_2: Raw signal should revert to NONE")

		// 3. Time advances past where the original signal would have been confirmed
		currentTime = baseTime.Add(501 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.4)
		assert.Nil(t, signal, "B_3: Signal should remain nil")
	})

	// --- Scenario C: Coarse Timestamps ---
	// Simulate events having the same timestamp, which might happen with CSV data.
	t.Run("Scenario C: Coarse Timestamps", func(t *testing.T) {
		// Reset engine state
		engine := newTestSignalEngine(holdDurationMs, 0.5, weights)

		// 1. Signal appears
		currentTime := baseTime
		signal := engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "C_1: Signal should not be confirmed immediately")

		// 2. Multiple events happen at the same time, signal is maintained
		signal = engine.Evaluate(currentTime, 0.6)
		assert.Nil(t, signal, "C_2: Signal should not be confirmed at same timestamp")

		// 3. Time advances past hold duration, but with a coarse jump
		currentTime = baseTime.Add(600 * time.Millisecond)
		signal = engine.Evaluate(currentTime, 0.6)
		if assert.NotNil(t, signal, "C_3: Signal should be confirmed after coarse time jump") {
			assert.Equal(t, SignalLong, signal.Type)
		}
	})
}
