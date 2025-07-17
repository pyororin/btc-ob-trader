package signal

/*
import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/your-org/obi-scalp-bot/internal/indicator"
)

func TestSignalEngine_Evaluate(t *testing.T) {
	engine := NewSignalEngine(300*time.Millisecond, 0.2, -0.2)

	// Test case 1: No signal initially
	signal := engine.Evaluate(indicator.OBIResult{OBI: 0.1, Timestamp: time.Now()})
	assert.Nil(t, signal, "Expected no signal for neutral OBI")

	// Test case 2: Long signal triggered and persists
	t.Run("LongSignal_Persists", func(t *testing.T) {
		engine.Reset()
		// Trigger long signal
		signal := engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		assert.Nil(t, signal, "Should not signal immediately")

		// Wait for hold duration
		time.Sleep(350 * time.Millisecond)
		signal = engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		if assert.NotNil(t, signal, "Expected long signal to persist") {
			assert.Equal(t, "LONG", signal.Type)
		}
	})

	// Test case 3: Short signal triggered and persists
	t.Run("ShortSignal_Persists", func(t *testing.T) {
		engine.Reset()
		// Trigger short signal
		signal := engine.Evaluate(indicator.OBIResult{OBI: -0.3, Timestamp: time.Now()})
		assert.Nil(t, signal, "Should not signal immediately")

		// Wait for hold duration
		time.Sleep(350 * time.Millisecond)
		signal = engine.Evaluate(indicator.OBIResult{OBI: -0.3, Timestamp: time.Now()})
		if assert.NotNil(t, signal, "Expected short signal to persist") {
			assert.Equal(t, "SHORT", signal.Type)
		}
	})

	// Test case 4: Signal drops if OBI returns to neutral
	t.Run("SignalDrops_NeutralOBI", func(t *testing.T) {
		engine.Reset()
		// Trigger long signal
		_ = engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		time.Sleep(100 * time.Millisecond)
		// OBI becomes neutral
		signal := engine.Evaluate(indicator.OBIResult{OBI: 0.1, Timestamp: time.Now()})
		assert.Nil(t, signal, "Signal should be reset to None")
		// Wait past hold duration
		time.Sleep(250 * time.Millisecond)
		signal = engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		assert.Nil(t, signal, "Should not signal as timer should have been reset")
	})

	// Test case 5: Signal flips from Long to Short
	t.Run("SignalFlips_LongToShort", func(t *testing.T) {
		engine.Reset()
		// Establish long
		_ = engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		time.Sleep(350 * time.Millisecond)
		signal := engine.Evaluate(indicator.OBIResult{OBI: 0.3, Timestamp: time.Now()})
		require.NotNil(t, signal)
		require.Equal(t, "LONG", signal.Type)

		// Flip to short
		signal = engine.Evaluate(indicator.OBIResult{OBI: -0.3, Timestamp: time.Now()})
		assert.Nil(t, signal, "Should not signal short immediately")
		time.Sleep(350 * time.Millisecond)
		signal = engine.Evaluate(indicator.OBIResult{OBI: -0.3, Timestamp: time.Now()})
		if assert.NotNil(t, signal) {
			assert.Equal(t, "SHORT", signal.Type)
		}
	})
}
*/
