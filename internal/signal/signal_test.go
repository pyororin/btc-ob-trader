package signal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/your-org/obi-scalp-bot/internal/config"
)

func TestSignalEngine_Evaluate_WithNormalization(t *testing.T) {
	weights := map[string]float64{
		"obi":        0.5,
		"ofi":        0.2,
		"cvd":        0.3,
		"microprice": 0.0,
	}
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
			HoldDurationMs:       0,
			CVDWindowMinutes:     1,
			CompositeThreshold:   0.5,
			OBIWeight:            weights["obi"],
			OFIWeight:            weights["ofi"],
			CVDWeight:            weights["cvd"],
			MicroPriceWeight:     weights["microprice"],
			NormalizationEnabled: true,
			NormalizationWindow:  3,
		},
		Volatility: config.VolConf{
			EWMALambda: 0.5,
		},
	}
	engine, err := NewSignalEngine(tradeCfg)
	if err != nil {
		t.Fatalf("Failed to create signal engine: %v", err)
	}

	// 1. Prime the normalization windows
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 0.1
	engine.cvdValue = 10
	_ = engine.Evaluate(time.Now(), 0.1) // obi: 0.1, ofi: 0.1, cvd: 10

	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 0.5
	engine.cvdValue = 50
	_ = engine.Evaluate(time.Now(), 0.5) // obi: 0.5, ofi: 0.5, cvd: 50

	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 1.0
	engine.cvdValue = 100
	_ = engine.Evaluate(time.Now(), 1.0) // obi: 1.0, ofi: 1.0, cvd: 100

	// At this point, the ranges should be:
	// OBI: [0.1, 1.0]
	// OFI: [0.1, 1.0]
	// CVD: [10, 100]

	// 2. Test a case that should now trigger a long signal
	// Values: obi=0.8, ofi=0.8, cvd=80
	// Normalized:
	// obi_norm = (0.8 - 0.1) / (1.0 - 0.1) = 0.7 / 0.9 = 0.777
	// ofi_norm = (0.8 - 0.1) / (1.0 - 0.1) = 0.7 / 0.9 = 0.777
	// cvd_norm = (80 - 10) / (100 - 10) = 70 / 90 = 0.777
	// Score = (0.777 * 0.5) + (0.777 * 0.2) + (0.777 * 0.3) = 0.777 * 1.0 = 0.777 > 0.5
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = 0.8
	engine.cvdValue = 80
	signal := engine.Evaluate(time.Now(), 0.8)
	if assert.NotNil(t, signal) {
		assert.Equal(t, SignalLong, signal.Type)
	}

	// 3. Test a case that should trigger a short signal
	// Update ranges to include negative values
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = -1.0
	engine.cvdValue = -100
	_ = engine.Evaluate(time.Now(), -1.0) // obi: -1.0, ofi: -1.0, cvd: -100
	// New ranges: OBI: [-1.0, 1.0], OFI: [-1.0, 1.0], CVD: [-100, 100]

	// Values: obi=-0.8, ofi=-0.8, cvd=-80
	// Normalized:
	// obi_norm = ((-0.8 - (-1.0)) / (1.0 - (-1.0))) * 2 - 1 = ((0.2 / 2.0) * 2) - 1 = 0.2 - 1 = -0.8
	// ofi_norm = -0.8
	// cvd_norm = -0.8
	// Score = (-0.8 * 0.5) + (-0.8 * 0.2) + (-0.8 * 0.3) = -0.8 < -0.5
	engine.UpdateMarketData(time.Now(), 100, 99.9, 100.1, 10, 10, nil)
	engine.ofiValue = -0.8
	engine.cvdValue = -80
	signal = engine.Evaluate(time.Now(), -0.8)
	if assert.NotNil(t, signal) {
		assert.Equal(t, SignalShort, signal.Type)
	}
}
