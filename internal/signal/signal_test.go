package signal

import (
	"math" // Added for almostEqual comparison
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
)

const floatEqualityThreshold = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < floatEqualityThreshold
}

func TestSignalEngine_Evaluate_LongSignal_Persists(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27}, // Short OBI threshold magnitude
		Volatility: config.VolConf{ // Ensure Volatility config is present
			DynamicOBI: config.DynamicOBIConf{Enabled: false}, // Dynamic disabled for this classic test
		},
	}
	engine, err := NewSignalEngine(cfg)
	if err != nil {
		t.Fatalf("NewSignalEngine() error = %v", err)
	}
	engine.config.SignalHoldDuration = 300 * time.Millisecond // Explicitly set for test clarity

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongBuy := 0.30
	currentPrice := 7000000.0 // Dummy price for UpdateMarketData

	// Simulate OBI values over time
	// Initial state: No signal
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, 0.1); ts != nil {
		t.Errorf("Expected nil signal, got %v at t=0", ts.Type)
	}

	// OBI crosses threshold, but not yet persisted
	currentTime = currentTime.Add(50 * time.Millisecond) // t = 50ms
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongBuy); ts != nil {
		t.Errorf("Expected nil signal, got %v at t=50ms (long not persisted)", ts.Type)
	}
	if engine.currentSignal != SignalLong {
		t.Errorf("Expected currentSignal to be Long, got %v", engine.currentSignal)
	}

	currentTime = currentTime.Add(100 * time.Millisecond) // t = 150ms
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongBuy); ts != nil {
		t.Errorf("Expected nil signal, got %v at t=150ms (long not persisted)", ts.Type)
	}

	currentTime = currentTime.Add(100 * time.Millisecond) // Current loop time: init_time + 250ms. currentSignal started at init_time + 50ms. Duration: 200ms.
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongBuy); ts != nil {
		t.Errorf("Expected nil signal, got %v at loop_time=250ms (duration 200ms, long not persisted)", ts.Type)
	}

	// OBI still above threshold, persistence duration (300ms) met.
	currentTime = currentTime.Add(100 * time.Millisecond) // Actual time: init_time + 350ms. Duration: 300ms.
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1) // Update price for TP/SL calc
	ts := engine.Evaluate(currentTime, obiStrongBuy)
	if ts == nil || ts.Type != SignalLong {
		var sigType SignalType = SignalNone
		if ts != nil { sigType = ts.Type }
		t.Errorf("Expected SignalLong, got %v at actual persistence time (duration %v)", sigType, currentTime.Sub(engine.currentSignalSince))
	} else {
		// Check TP/SL values
		expectedTP := currentPrice + cfg.Long.TP
		expectedSL := currentPrice + cfg.Long.SL
		if !almostEqual(ts.TakeProfit, expectedTP) || !almostEqual(ts.StopLoss, expectedSL) {
			t.Errorf("Long signal TP/SL mismatch: got TP %v, SL %v; want TP %v, SL %v. Entry: %v", ts.TakeProfit, ts.StopLoss, expectedTP, expectedSL, ts.EntryPrice)
		}
	}
	if engine.lastSignal != SignalLong {
		t.Errorf("Expected lastSignal to be Long after confirmation, got %v", engine.lastSignal)
	}

	// Call again, should not re-trigger immediately if already triggered
	currentTime = currentTime.Add(50 * time.Millisecond) // Actual time: init_time + 400ms
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongBuy); ts != nil {
		t.Errorf("Expected nil signal (already triggered), got %v", ts.Type)
	}
}

func TestSignalEngine_Evaluate_LongSignal_DoesNotPersist(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongBuy := 0.30
	obiNeutral := 0.10
	currentPrice := 7000000.0

	// OBI crosses threshold
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongBuy); ts != nil {
		t.Errorf("Expected nil signal, got %v (long not persisted yet)", ts.Type)
	}

	// OBI drops before persistence duration
	currentTime = currentTime.Add(100 * time.Millisecond) // Total 150ms in StrongBuy
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiNeutral); ts != nil {
		t.Errorf("Expected nil signal, got %v (long dropped before persistence)", ts.Type)
	}
	if engine.currentSignal != SignalNone {
		t.Errorf("Expected currentSignal to be None after drop, got %v", engine.currentSignal)
	}
}

func TestSignalEngine_Evaluate_ShortSignal_Persists(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25, TP: 200, SL: -100},
		Short: config.StrategyConf{OBIThreshold: 0.27, TP: 250, SL: -120},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongSell := -0.30
	currentPrice := 7000000.0

	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	engine.Evaluate(currentTime, 0.0)

	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongSell); ts != nil {
		t.Errorf("Expected nil signal, got %v (short not persisted, currentSignalSince: %v)", ts.Type, engine.currentSignalSince)
	}

	currentTime = currentTime.Add(engine.config.SignalHoldDuration - 1*time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiStrongSell); ts != nil {
		t.Errorf("Expected nil signal, got %v (short not quite persisted, duration %v)", ts.Type, currentTime.Sub(engine.currentSignalSince))
	}

	currentTime = currentTime.Add(1 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1) // Update price for TP/SL
	ts := engine.Evaluate(currentTime, obiStrongSell)
	if ts == nil || ts.Type != SignalShort {
		var sigType SignalType = SignalNone
		if ts != nil { sigType = ts.Type }
		t.Errorf("Expected SignalShort, got %v (short should persist, duration %v)", sigType, currentTime.Sub(engine.currentSignalSince))
	} else {
		expectedTP := currentPrice - cfg.Short.TP
		expectedSL := currentPrice - cfg.Short.SL // SL is negative, so currentPrice - (-val) = currentPrice + val
		if !almostEqual(ts.TakeProfit, expectedTP) || !almostEqual(ts.StopLoss, expectedSL) {
			t.Errorf("Short signal TP/SL mismatch: got TP %v, SL %v; want TP %v, SL %v. Entry: %v", ts.TakeProfit, ts.StopLoss, expectedTP, expectedSL, ts.EntryPrice)
		}
	}
}

func TestSignalEngine_Evaluate_ShortSignal_DoesNotPersist(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongSell := -0.30
	obiNeutral := 0.0
	currentPrice := 7000000.0

	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	engine.Evaluate(currentTime, obiNeutral)

	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	engine.Evaluate(currentTime, obiStrongSell)

	currentTime = currentTime.Add(100 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiNeutral); ts != nil {
		t.Errorf("Expected nil signal, got %v (short dropped before persistence)", ts.Type)
	}
}


func TestSignalEngine_Evaluate_NoSignal(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiNeutral := 0.10
	currentPrice := 7000000.0

	for i := 0; i < 10; i++ {
		currentTime = currentTime.Add(50 * time.Millisecond)
		engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
		if ts := engine.Evaluate(currentTime, obiNeutral); ts != nil {
			t.Errorf("Expected nil signal, got %v for neutral OBI", ts.Type)
			break
		}
	}
}

func TestSignalEngine_Evaluate_SignalRecovery(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25, TP: 10, SL: -10},
		Short: config.StrategyConf{OBIThreshold: 0.27, TP: 10, SL: -10},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 100 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiBuy := 0.30
	obiNeutral := 0.10
	currentPrice := 7000000.0

	// 1. Initial Long signal confirmation
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	engine.Evaluate(currentTime, obiBuy)
	currentTime = currentTime.Add(100 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	ts1 := engine.Evaluate(currentTime, obiBuy)
	if ts1 == nil || ts1.Type != SignalLong {
		var sigType SignalType = SignalNone
		if ts1 != nil { sigType = ts1.Type}
		t.Errorf("Expected SignalLong, got %v (initial confirmation)", sigType)
	}

	// 2. Signal drops
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	if ts := engine.Evaluate(currentTime, obiNeutral); ts != nil {
		t.Errorf("Expected nil signal, got %v (signal dropped)", ts.Type)
	}
	if engine.lastSignal != SignalNone {
		t.Errorf("Expected lastSignal to be cleared to SignalNone, got %v", engine.lastSignal)
	}

	// 3. Signal recovers and re-confirms
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	engine.Evaluate(currentTime, obiBuy)
	currentTime = currentTime.Add(100 * time.Millisecond)
	engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
	ts2 := engine.Evaluate(currentTime, obiBuy)
	if ts2 == nil || ts2.Type != SignalLong {
		var sigType SignalType = SignalNone
		if ts2 != nil { sigType = ts2.Type}
		t.Errorf("Expected SignalLong, got %v (re-confirmation)", sigType)
	}
}


func TestSignalEngine_DoD_Long5_Short5_Signals(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25, TP: 10, SL: -10},
		Short: config.StrategyConf{OBIThreshold: 0.27, TP: 10, SL: -10},
		Volatility: config.VolConf{
			DynamicOBI: config.DynamicOBIConf{Enabled: false},
		},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 100 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiBuy := 0.30
	obiSell := -0.30
	obiNeutral := 0.0
	currentPrice := 7000000.0

	longSignalCount := 0
	shortSignalCount := 0

	holdDuration := engine.config.SignalHoldDuration

	// Helper function for DoD test steps
	runDodStep := func(stepName string, timeAdvance time.Duration, obiValue float64, expectedSigType SignalType) {
		t.Helper()
		currentTime = currentTime.Add(timeAdvance)
		engine.UpdateMarketData(currentTime, currentPrice, currentPrice-1, currentPrice+1, 1, 1)
		ts := engine.Evaluate(currentTime, obiValue)

		actualSigType := SignalNone
		if ts != nil {
			actualSigType = ts.Type
		}

		if actualSigType != expectedSigType {
			t.Errorf("Step %s: Expected signal type %v, got %v at time %v (obi: %.2f, currentSignal: %s, currentSignalSince: %s, lastSignal: %s, lastSignalTime: %s)",
				stepName, expectedSigType, actualSigType, currentTime.Sub(time.Date(2024,1,1,0,0,0,0,time.UTC)), obiValue, engine.currentSignal, engine.currentSignalSince.Format(time.RFC3339Nano), engine.lastSignal, engine.lastSignalTime.Format(time.RFC3339Nano))
		}
		if actualSigType == SignalLong {
			longSignalCount++
		}
		if actualSigType == SignalShort {
			shortSignalCount++
		}
	}

	// Initial state: Neutral OBI, evaluate to set a baseline
	runDodStep("Initial_Neutral", 0, obiNeutral, SignalNone)

	for i := 0; i < 5; i++ {
		// Long Signal Cycle
		runDodStep("L_Start", 50*time.Millisecond, obiBuy, SignalNone)
		runDodStep("L_Confirm", holdDuration, obiBuy, SignalLong)
		runDodStep("L_Clear_Start", 50*time.Millisecond, obiNeutral, SignalNone)
		runDodStep("L_Clear_Persist", holdDuration, obiNeutral, SignalNone)

		// Short Signal Cycle
		runDodStep("S_Start", 50*time.Millisecond, obiSell, SignalNone)
		runDodStep("S_Confirm", holdDuration, obiSell, SignalShort)
		runDodStep("S_Clear_Start", 50*time.Millisecond, obiNeutral, SignalNone)
		runDodStep("S_Clear_Persist", holdDuration, obiNeutral, SignalNone)
	}

	if longSignalCount < 5 {
		t.Errorf("DoD fail: Expected at least 5 Long signals, got %d", longSignalCount)
	}
	if shortSignalCount < 5 {
		t.Errorf("DoD fail: Expected at least 5 Short signals, got %d", shortSignalCount)
	}
}

func TestSignalEngine_DynamicOBIThresholds(t *testing.T) {
	baseLongOBI := 0.25
	baseShortOBI := 0.27
	volatilityFactor := 1.0
	minFactor := 0.5
	maxFactor := 2.0

	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: baseLongOBI},
		Short: config.StrategyConf{OBIThreshold: baseShortOBI},
		Volatility: config.VolConf{
			EWMALambda: 0.1,
			DynamicOBI: config.DynamicOBIConf{
				Enabled:            true,
				VolatilityFactor:   volatilityFactor,
				MinThresholdFactor: minFactor,
				MaxThresholdFactor: maxFactor,
			},
		},
	}
	engine, err := NewSignalEngine(cfg)
	if err != nil {
		t.Fatalf("NewSignalEngine() error = %v", err)
	}
	engine.config.SignalHoldDuration = 100 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	initialMidPrice := 7000000.0
	dummyBid := initialMidPrice - 50
	dummyAsk := initialMidPrice + 50
	dummySize := 1.0

	// Initial state: No volatility, thresholds should be base
	engine.UpdateMarketData(currentTime, initialMidPrice, dummyBid, dummyAsk, dummySize, dummySize)
	if !almostEqual(engine.GetCurrentLongOBIThreshold(), baseLongOBI) {
		t.Errorf("Initial long threshold: got %v, want %v", engine.GetCurrentLongOBIThreshold(), baseLongOBI)
	}
	if !almostEqual(engine.GetCurrentShortOBIThreshold(), baseShortOBI) {
		t.Errorf("Initial short threshold: got %v, want %v", engine.GetCurrentShortOBIThreshold(), baseShortOBI)
	}

	// Simulate price increase to generate some volatility
	price1 := initialMidPrice * 1.01 // 1% increase
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, price1, price1-50, price1+50, dummySize, dummySize)
	stdDev1 := engine.volatilityCalc.GetEWMStandardDeviation()

	price2 := price1 * 1.01 // Another 1% increase
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, price2, price2-50, price2+50, dummySize, dummySize)
	stdDev2 := engine.volatilityCalc.GetEWMStandardDeviation()

	if !(stdDev2 > stdDev1 && stdDev1 >= 0) {
		// Allow stdDev1 to be 0 if only one non-zero return has been processed and lambda is such
		// or if the first return itself was zero (e.g. price didn't change from vc.prevPrice).
		// The VolatilityCalculator's Update method initializes prevPrice with the first price, so the first call to Update
		// will result in 0 return and 0 stddev. The *second* call with a *different* price will generate the first non-zero return.
		// So stdDev1 (after first price1 update) might be 0 if initialMidPrice was used to init vc.prevPrice.
		// Let's re-check logic:
		// vc is new. Update(initialMidPrice) -> sets prevPrice=initialMidPrice, returns 0,0. This happens implicitly in first UpdateMarketData.
		// Update(price1) -> ret=(price1-initialMidPrice)/initialMidPrice. ewmaRet, stdDev1 calculated. stdDev1 should be >0.
		// Update(price2) -> ret=(price2-price1)/price1. ewmaRet, stdDev2 calculated.
		t.Logf("Initial price for vol calc: %f", initialMidPrice) // This is the price at which vc.prevPrice is set
		t.Logf("Price1: %f, Return1: %f", price1, (price1-initialMidPrice)/initialMidPrice)
		t.Logf("stdDev1: %v", stdDev1)
		t.Logf("Price2: %f, Return2: %f", price2, (price2-price1)/price1)
        t.Logf("stdDev2: %v", stdDev2)
		if !(stdDev2 > stdDev1) && stdDev1 == 0 && stdDev2 > 0 {
            // This is acceptable: first update (price1) generated the first non-zero std dev (stdDev2 in this context of variable naming).
            // The issue might be that stdDev1 corresponds to the update with `initialMidPrice` if UpdateMarketData calls were structured differently.
            // Test as is, if it fails, it implies VolatilityCalculator state after first UpdateMarketData was not as expected.
        } else if !(stdDev2 > stdDev1) {
			t.Errorf("Expected standard deviation to increase or stay same with price changes, got stdDev1=%v, stdDev2=%v", stdDev1, stdDev2)
		}
	}


	expectedLongThresh := baseLongOBI * (1 + volatilityFactor*stdDev2)
	expectedLongThresh = math.Max(baseLongOBI*minFactor, math.Min(expectedLongThresh, baseLongOBI*maxFactor))

	if !almostEqual(engine.GetCurrentLongOBIThreshold(), expectedLongThresh) {
		t.Errorf("Dynamic long threshold: got %v, want %v (stdDev: %v)", engine.GetCurrentLongOBIThreshold(), expectedLongThresh, stdDev2)
	}

	// Test signal with dynamic threshold
	triggerObiBuy := engine.GetCurrentLongOBIThreshold() + 0.01

	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, price2, price2-50, price2+50, dummySize, dummySize)
	if ts := engine.Evaluate(currentTime, triggerObiBuy); ts != nil {
		t.Errorf("Expected nil signal (not persisted), got %v", ts.Type)
	}

	currentTime = currentTime.Add(engine.config.SignalHoldDuration)
	engine.UpdateMarketData(currentTime, price2, price2-50, price2+50, dummySize, dummySize)
	tsConfirm := engine.Evaluate(currentTime, triggerObiBuy)
	if tsConfirm == nil || tsConfirm.Type != SignalLong {
		var sigType SignalType = SignalNone
		if tsConfirm != nil {
			sigType = tsConfirm.Type
		}
		t.Errorf("Expected SignalLong (with dynamic threshold), got %v. Threshold: %v, OBI: %v", sigType, engine.GetCurrentLongOBIThreshold(), triggerObiBuy)
	}

	// Test Max clamping
	extremePrice := price2 * 1.5
	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.UpdateMarketData(currentTime, extremePrice, extremePrice-50, extremePrice+50, dummySize, dummySize) // Update with extremePrice first
	extremeStdDev := engine.volatilityCalc.GetEWMStandardDeviation() // Initialize std dev after first extreme update
	prevPriceForVol := extremePrice // prevPriceForVol を extremePrice で初期化

	for i:=0; i<10; i++ { // More large jumps to ensure std dev grows significantly
		nextPrice := prevPriceForVol * (1.1 + float64(i)*0.05) // Increasing jump size
		engine.UpdateMarketData(currentTime, nextPrice, nextPrice-50, nextPrice+50, dummySize, dummySize)
		extremeStdDev = engine.volatilityCalc.GetEWMStandardDeviation()
		prevPriceForVol = nextPrice
		currentTime = currentTime.Add(10 * time.Millisecond)
	}

	maxExpectedLong := baseLongOBI * maxFactor
	actualCalcThreshUnclamped := baseLongOBI * (1 + volatilityFactor*extremeStdDev)
	t.Logf("StdDev for clamping test: %v, Calculated (unclamped) threshold: %v, Max clamp: %v", extremeStdDev, actualCalcThreshUnclamped, maxExpectedLong)

	// Check if the current threshold is correctly clamped at the max value
	if !almostEqual(engine.GetCurrentLongOBIThreshold(), maxExpectedLong) {
		// If it's not equal to maxExpectedLong, it might be because actualCalcThreshUnclamped is still less than maxExpectedLong
		if actualCalcThreshUnclamped >= maxExpectedLong { // Should be clamped
			t.Errorf("Max clamped dynamic long threshold: got %v, want %v (stdDev: %v, calculated unclamped: %v)",
				engine.GetCurrentLongOBIThreshold(), maxExpectedLong, extremeStdDev, actualCalcThreshUnclamped)
		} else { // Should be actualCalcThreshUnclamped
			if !almostEqual(engine.GetCurrentLongOBIThreshold(), actualCalcThreshUnclamped) {
				t.Errorf("Dynamic long threshold (below max clamp): got %v, want %v (stdDev: %v)",
					engine.GetCurrentLongOBIThreshold(), actualCalcThreshUnclamped, extremeStdDev)
			}
		}
	}


	// Simulate zero volatility after some activity (prices stabilize)
	stablePrice := prevPriceForVol
	for i := 0; i < 50; i++ { // Many updates with same price to drive std dev down
		currentTime = currentTime.Add(10 * time.Millisecond)
		engine.UpdateMarketData(currentTime, stablePrice, stablePrice-50, stablePrice+50, dummySize, dummySize)
	}
	stdDevStable := engine.volatilityCalc.GetEWMStandardDeviation()

	minExpectedLongClamped := baseLongOBI * minFactor
    calculatedLowVolThreshold := baseLongOBI * (1 + volatilityFactor*stdDevStable)
    expectedClampedLowVolThreshold := math.Max(minExpectedLongClamped, math.Min(calculatedLowVolThreshold, maxExpectedLong))

	if !almostEqual(engine.GetCurrentLongOBIThreshold(), expectedClampedLowVolThreshold) {
		t.Errorf("Low volatility dynamic long threshold: got %v, want %v (stdDev: %v, min_clamp_val: %v, calc_before_clamp: %v)",
			engine.GetCurrentLongOBIThreshold(), expectedClampedLowVolThreshold, stdDevStable, minExpectedLongClamped, calculatedLowVolThreshold)
	}
}
