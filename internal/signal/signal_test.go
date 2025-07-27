package signal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/your-org/obi-scalp-bot/internal/config"
)

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
