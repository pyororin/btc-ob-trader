package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestLoadConfig(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "config-test")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a dummy config file
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Define a sample config with the new Risk struct
	sampleConfig := Config{
		Pair:        "btc_jpy",
		SpreadLimit: 100,
		Risk: RiskConfig{
			MaxDrawdownPercent: 10.0,
			MaxPositionJPY:     500000.0,
		},
		// Add other necessary fields to make the config valid
	}

	yamlData, err := yaml.Marshal(&sampleConfig)
	assert.NoError(t, err)

	err = os.WriteFile(configPath, yamlData, 0644)
	assert.NoError(t, err)

	// Test loading the config
	loadedCfg, err := LoadConfig(configPath)
	assert.NoError(t, err)

	// Assert that the risk config is loaded correctly
	assert.Equal(t, 10.0, loadedCfg.Risk.MaxDrawdownPercent)
	assert.Equal(t, 500000.0, loadedCfg.Risk.MaxPositionJPY)
	assert.Equal(t, "btc_jpy", loadedCfg.Pair)
	assert.Equal(t, float64(100), loadedCfg.SpreadLimit)

	// Test with environment variable overrides
	os.Setenv("LOG_LEVEL", "debug")
	loadedCfg, err = LoadConfig(configPath)
	assert.NoError(t, err)
	assert.Equal(t, "debug", loadedCfg.LogLevel)
	os.Unsetenv("LOG_LEVEL")
}
