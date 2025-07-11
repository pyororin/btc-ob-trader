package signal

import (
	"testing"
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
)

func TestSignalEngine_Evaluate_LongSignal_Persists(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27}, // Short OBI threshold magnitude
	}
	engine, err := NewSignalEngine(cfg)
	if err != nil {
		t.Fatalf("NewSignalEngine() error = %v", err)
	}
	engine.config.SignalHoldDuration = 300 * time.Millisecond // Explicitly set for test clarity

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongBuy := 0.30

	// Simulate OBI values over time
	// Initial state: No signal
	if sig := engine.Evaluate(currentTime, 0.1); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v at t=0", sig)
	}

	// OBI crosses threshold, but not yet persisted
	currentTime = currentTime.Add(50 * time.Millisecond) // t = 50ms
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v at t=50ms (long not persisted)", sig)
	}
	if engine.currentSignal != SignalLong {
		t.Errorf("Expected currentSignal to be Long, got %v", engine.currentSignal)
	}

	currentTime = currentTime.Add(100 * time.Millisecond) // t = 150ms
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v at t=150ms (long not persisted)", sig)
	}

	currentTime = currentTime.Add(100 * time.Millisecond) // Current loop time: init_time + 250ms. currentSignal started at init_time + 50ms. Duration: 200ms.
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v at loop_time=250ms (duration 200ms, long not persisted)", sig)
	}

	// OBI still above threshold, persistence duration (300ms) met.
	// currentSignalSince was set at init_time + 50ms.
	// Persistence is 300ms. So, signal is confirmed when actual time is (init_time + 50ms) + 300ms = init_time + 350ms.
	// Current actual time is init_time + 250ms. We need to advance by 100ms more.
	currentTime = currentTime.Add(100 * time.Millisecond) // Actual time: init_time + 350ms. Duration: 300ms.
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalLong {
		t.Errorf("Expected SignalLong, got %v at actual persistence time (duration %v)", sig, currentTime.Sub(engine.currentSignalSince))
	}
	if engine.lastSignal != SignalLong { // This should now be Long
		t.Errorf("Expected lastSignal to be Long after confirmation, got %v", engine.lastSignal)
	}

	// Call again, should not re-trigger immediately if already triggered
	currentTime = currentTime.Add(50 * time.Millisecond) // Actual time: init_time + 400ms
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalNone {
		t.Errorf("Expected SignalNone (already triggered), got %v", sig)
	}
}

func TestSignalEngine_Evaluate_LongSignal_DoesNotPersist(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongBuy := 0.30
	obiNeutral := 0.10

	// OBI crosses threshold
	currentTime = currentTime.Add(50 * time.Millisecond)
	if sig := engine.Evaluate(currentTime, obiStrongBuy); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (long not persisted yet)", sig)
	}

	// OBI drops before persistence duration
	currentTime = currentTime.Add(100 * time.Millisecond) // Total 150ms in StrongBuy
	if sig := engine.Evaluate(currentTime, obiNeutral); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (long dropped before persistence)", sig)
	}
	if engine.currentSignal != SignalNone { // currentSignal should reset
		t.Errorf("Expected currentSignal to be None after drop, got %v", engine.currentSignal)
	}
}

func TestSignalEngine_Evaluate_ShortSignal_Persists(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27}, // Short OBI threshold magnitude
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongSell := -0.30 // OBI for strong sell

	engine.Evaluate(currentTime, 0.0) // Start neutral

	// OBI crosses short threshold
	currentTime = currentTime.Add(50 * time.Millisecond) // actual_time = init_time + 50ms
	if sig := engine.Evaluate(currentTime, obiStrongSell); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (short not persisted, currentSignalSince: %v)", sig, engine.currentSignalSince)
	}

	// Maintain short signal
	// currentSignalSince is init_time + 50ms. Current actual time is init_time + 50ms.
	// Need to advance so that duration is just under 300ms (SignalHoldDuration).
	// So, advance by SignalHoldDuration - 1ms = 299ms.
	// currentTime will be (init_time + 50ms) + 299ms = init_time + 349ms.
	// Duration will be (init_time + 349ms) - (init_time + 50ms) = 299ms.
	currentTime = currentTime.Add(engine.config.SignalHoldDuration - 1*time.Millisecond)
	if sig := engine.Evaluate(currentTime, obiStrongSell); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (short not quite persisted, duration %v)", sig, currentTime.Sub(engine.currentSignalSince))
	}

	// Advance by 1ms more to meet SignalHoldDuration.
	// currentTime will be (init_time + 349ms) + 1ms = init_time + 350ms.
	// Duration will be 300ms.
	currentTime = currentTime.Add(1 * time.Millisecond)
	if sig := engine.Evaluate(currentTime, obiStrongSell); sig != SignalShort {
		t.Errorf("Expected SignalShort, got %v (short should persist, duration %v)", sig, currentTime.Sub(engine.currentSignalSince))
	}
}

func TestSignalEngine_Evaluate_ShortSignal_DoesNotPersist(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiStrongSell := -0.30
	obiNeutral := 0.0

	engine.Evaluate(currentTime, obiNeutral)

	currentTime = currentTime.Add(50 * time.Millisecond)
	engine.Evaluate(currentTime, obiStrongSell) // Potential short

	currentTime = currentTime.Add(100 * time.Millisecond) // Total 100ms in StrongSell
	if sig := engine.Evaluate(currentTime, obiNeutral); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (short dropped before persistence)", sig)
	}
}


func TestSignalEngine_Evaluate_NoSignal(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 300 * time.Millisecond

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiNeutral := 0.10

	for i := 0; i < 10; i++ { // Simulate multiple evaluations
		currentTime = currentTime.Add(50 * time.Millisecond)
		if sig := engine.Evaluate(currentTime, obiNeutral); sig != SignalNone {
			t.Errorf("Expected SignalNone, got %v for neutral OBI", sig)
			break
		}
	}
}

func TestSignalEngine_Evaluate_SignalRecovery(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 100 * time.Millisecond // Shorter duration for this test

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiBuy := 0.30
	obiNeutral := 0.10

	// 1. Initial Long signal confirmation
	currentTime = currentTime.Add(50 * time.Millisecond) // event_start_1 = 50ms
	engine.Evaluate(currentTime, obiBuy)                 // currentSignal = Long
	currentTime = currentTime.Add(100 * time.Millisecond) // event_start_1 + 100ms = 150ms. Persisted.
	if sig := engine.Evaluate(currentTime, obiBuy); sig != SignalLong {
		t.Errorf("Expected SignalLong, got %v (initial confirmation)", sig)
	}

	// 2. Signal drops
	currentTime = currentTime.Add(50 * time.Millisecond) // 200ms
	if sig := engine.Evaluate(currentTime, obiNeutral); sig != SignalNone {
		t.Errorf("Expected SignalNone, got %v (signal dropped)", sig)
	}
	if engine.lastSignal != SignalNone { // lastSignal should be cleared when current drops to None
		t.Errorf("Expected lastSignal to be cleared to SignalNone, got %v", engine.lastSignal)
	}


	// 3. Signal recovers and re-confirms
	currentTime = currentTime.Add(50 * time.Millisecond)  // event_start_2 = 250ms
	engine.Evaluate(currentTime, obiBuy)                  // currentSignal = Long again
	currentTime = currentTime.Add(100 * time.Millisecond) // event_start_2 + 100ms = 350ms. Persisted.
	if sig := engine.Evaluate(currentTime, obiBuy); sig != SignalLong {
		t.Errorf("Expected SignalLong, got %v (re-confirmation)", sig)
	}
}


func TestSignalEngine_DoD_Long5_Short5_Signals(t *testing.T) {
	cfg := &config.Config{
		Long:  config.StrategyConf{OBIThreshold: 0.25},
		Short: config.StrategyConf{OBIThreshold: 0.27},
	}
	engine, _ := NewSignalEngine(cfg)
	engine.config.SignalHoldDuration = 100 * time.Millisecond // Shorter duration for easier DoD test

	currentTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	obiBuy := 0.30
	obiSell := -0.30
	obiNeutral := 0.0

	longSignalCount := 0
	shortSignalCount := 0

	holdDuration := engine.config.SignalHoldDuration // 100ms for this test

	// Helper function for DoD test steps
	runDodStep := func(stepName string, timeAdvance time.Duration, obiValue float64, expectedSig SignalType) {
		t.Helper()
		currentTime = currentTime.Add(timeAdvance)
		sig := engine.Evaluate(currentTime, obiValue)
		if sig != expectedSig {
			t.Errorf("Step %s: Expected signal %v, got %v at time %v (obi: %.2f, currentSignal: %s, currentSignalSince: %s, lastSignal: %s, lastSignalTime: %s)",
				stepName, expectedSig, sig, currentTime.Sub(time.Date(2024,1,1,0,0,0,0,time.UTC)), obiValue, engine.currentSignal, engine.currentSignalSince.Format(time.RFC3339Nano), engine.lastSignal, engine.lastSignalTime.Format(time.RFC3339Nano))
		}
		if sig == SignalLong {
			longSignalCount++
		}
		if sig == SignalShort {
			shortSignalCount++
		}
	}

	// Initial state: Neutral OBI, evaluate to set a baseline
	runDodStep("Initial_Neutral", 0, obiNeutral, SignalNone)

	for i := 0; i < 5; i++ {
		// Long Signal Cycle
		runDodStep("L_Start", 50*time.Millisecond, obiBuy, SignalNone)      // Potential Long starts
		runDodStep("L_Confirm", holdDuration, obiBuy, SignalLong)           // Long persists and confirms
		runDodStep("L_Clear_Start", 50*time.Millisecond, obiNeutral, SignalNone) // Signal clears (becomes None)
		runDodStep("L_Clear_Persist", holdDuration, obiNeutral, SignalNone) // Ensure it stays None after clearing

		// Short Signal Cycle
		runDodStep("S_Start", 50*time.Millisecond, obiSell, SignalNone)     // Potential Short starts
		runDodStep("S_Confirm", holdDuration, obiSell, SignalShort)         // Short persists and confirms
		runDodStep("S_Clear_Start", 50*time.Millisecond, obiNeutral, SignalNone) // Signal clears
		runDodStep("S_Clear_Persist", holdDuration, obiNeutral, SignalNone) // Ensure it stays None
	}

	if longSignalCount < 5 {
		t.Errorf("DoD fail: Expected at least 5 Long signals, got %d", longSignalCount)
	}
	if shortSignalCount < 5 {
		t.Errorf("DoD fail: Expected at least 5 Short signals, got %d", shortSignalCount)
	}
}
