// Package signal provides the logic for generating trading signals.
package signal

import (
	"time"

	"github.com/your-org/obi-scalp-bot/internal/config"
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
// This might be extended if more config parameters are needed directly by the engine.
type EngineConfig struct {
	LongOBIBThreshold  float64
	ShortOBIBThreshold float64
	SignalHoldDuration time.Duration // Duration for which a signal must persist
}

// SignalEngine evaluates market data and generates trading signals.
type SignalEngine struct {
	config             EngineConfig
	lastSignal         SignalType
	lastSignalTime     time.Time
	currentSignal      SignalType
	currentSignalSince time.Time
}

// NewSignalEngine creates a new SignalEngine.
// It will load necessary configurations.
func NewSignalEngine(cfg *config.Config) (*SignalEngine, error) {
	// Example: Convert config.Config to EngineConfig
	// This assumes your main config struct `config.Config` has fields like
	// cfg.Long.OBIThreshold, cfg.Short.OBIThreshold.
	// And a way to define SignalHoldDuration, e.g. directly in config.yaml or as a constant.
	// For now, let's assume a fixed hold duration for simplicity, it can be added to config.yaml later.
	signalHoldDuration := 300 * time.Millisecond // As per T-04: "300 ms 継続ロジック"

	engineCfg := EngineConfig{
		LongOBIBThreshold:  cfg.Long.OBIThreshold,
		ShortOBIBThreshold: cfg.Short.OBIThreshold,
		SignalHoldDuration: signalHoldDuration,
	}

	return &SignalEngine{
		config:             engineCfg,
		lastSignal:         SignalNone,
		currentSignal:      SignalNone,
		// Initialize time fields to zero, they will be set on first evaluation or signal change.
	}, nil
}

// Evaluate evaluates the current market data and returns a trading signal
// that has persisted for the configured duration.
// It's intended to be called periodically (e.g., every 50ms).
func (e *SignalEngine) Evaluate(currentTime time.Time, obiValue float64) SignalType {
	// 1. Determine the raw potential signal based on current OBI value
	rawSignal := SignalNone
	if obiValue >= e.config.LongOBIBThreshold {
		rawSignal = SignalLong
	} else if obiValue <= -e.config.ShortOBIBThreshold { // Negative of the threshold for short
		rawSignal = SignalShort
	}

	if rawSignal != e.currentSignal { // Signal state is changing
		e.currentSignal = rawSignal
		e.currentSignalSince = currentTime

		if rawSignal == SignalNone {
			// If the new state is "No Signal", then any previously confirmed signal is now definitely over.
			// Reset lastSignal so that if the same type of signal appears again, it's treated as a new event.
			e.lastSignal = SignalNone
		}
		return SignalNone // Not confirmed yet, or just ended.
	}

	// At this point, rawSignal == e.currentSignal (signal state is stable since last call)

	if e.currentSignal == SignalNone {
		// Consistently no signal. Ensure lastSignal reflects this.
		if e.lastSignal != SignalNone { // Should have been caught above if it *just* became None.
			e.lastSignal = SignalNone   // Defensive.
		}
		return SignalNone
	}

	// currentSignal is active (Long or Short) and stable. Check persistence.
	if currentTime.Sub(e.currentSignalSince) >= e.config.SignalHoldDuration {
		// It has persisted long enough.
		// Should we emit it as a *new* confirmed signal event?
		if e.lastSignal != e.currentSignal {
			// Yes, because the last *emitted* signal was different (or None).
			e.lastSignal = e.currentSignal
			e.lastSignalTime = currentTime // Track when this signal was confirmed.
			return e.currentSignal
		}
		// No, it's the same signal event that was already emitted.
		return SignalNone // Already reported.
	}

	// Active and stable, but not yet persisted long enough.
	return SignalNone
}
