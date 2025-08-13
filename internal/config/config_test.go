// Package config_test tests the config package.
package config_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/your-org/obi-scalp-bot/internal/config"
)


// createTestAppConfigFile creates a dummy app config file for testing.
func createTestAppConfigFile(path, logLevel string) {
	yamlContent := fmt.Sprintf(`
log_level: "%s"
`, logLevel)
	err := os.WriteFile(path, []byte(yamlContent), 0644)
	if err != nil {
		panic(err)
	}
}

// createTestTradeConfigFile creates a dummy trade config file for testing.
func createTestTradeConfigFile(path string, compositeThreshold float64) {
	yamlContent := fmt.Sprintf(`
pair: "btc_jpy"
signal:
  composite_threshold: %.2f
`, compositeThreshold)
	err := os.WriteFile(path, []byte(yamlContent), 0644)
	if err != nil {
		panic(err)
	}
}

// TestConfigReloading tests the dynamic reloading of configuration.
func TestConfigReloading(t *testing.T) {
	tmpDir := t.TempDir()
	appConfigPath := filepath.Join(tmpDir, "app_config.yaml")
	tradeConfigPath := filepath.Join(tmpDir, "trade_config.yaml")

	// 1. Create and load initial config
	createTestAppConfigFile(appConfigPath, "info")
	createTestTradeConfigFile(tradeConfigPath, 0.1)
	initialCfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	require.NoError(t, err, "Initial config loading should succeed")
	require.Equal(t, 0.1, initialCfg.Trade.Signal.CompositeThreshold, "Initial composite threshold should be 0.1")

	// 2. Concurrently access config while reloading
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond) // Give some time for the main thread to start reading
		t.Log("Goroutine: Reloading config...")
		createTestTradeConfigFile(tradeConfigPath, 0.2) // Update the trade config file
		_, err := config.ReloadConfig(appConfigPath, tradeConfigPath)
		assert.NoError(t, err, "Config reloading should succeed")
		t.Log("Goroutine: Config reloaded.")
	}()

	// Continuously read the config to check for race conditions and see the update
	var finalThreshold float64
	for i := 0; i < 100; i++ {
		currentCfg := config.GetConfig()
		if currentCfg.Trade.Signal.CompositeThreshold == 0.2 {
			finalThreshold = currentCfg.Trade.Signal.CompositeThreshold
			break
		}
		time.Sleep(10 * time.Millisecond) // Simulate work
	}
	wg.Wait() // Wait for the reloading goroutine to finish

	// 3. Verify the config was updated
	assert.Equal(t, 0.2, finalThreshold, "Composite threshold should have been updated to 0.2")

	finalCfg := config.GetConfig()
	assert.Equal(t, 0.2, finalCfg.Trade.Signal.CompositeThreshold, "Final config check should show updated composite threshold")
}

// Helper function to create a dummy config file with specific content
func createDummyConfigFile(t *testing.T, path, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

// TestLoadConfig_EnvVarOverride tests if environment variables correctly override yaml values.
func TestLoadConfig_EnvVarOverride(t *testing.T) {
	// Use t.Setenv to ensure environment variables are scoped to this test.
	// This prevents interference from the host environment or .env files.
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DB_HOST", "db.from.env")
	t.Setenv("DB_USER", "user_from_env")
	t.Setenv("COINCHECK_API_KEY", "key_from_env")

	// Explicitly unset a variable to ensure it's not present.
	// This is a robust way to guarantee the test's environment.
	t.Setenv("DB_PASSWORD", "")
	// For this test, we also need to make sure cái `DOTENV_PATH` is not set,
	// so it doesn't try to load a .env file from a default location.
	t.Setenv("DOTENV_PATH", "")

	tmpDir := t.TempDir()
	appConfigPath := filepath.Join(tmpDir, "app_config.yaml")
	tradeConfigPath := filepath.Join(tmpDir, "trade_config.yaml")

	// Initial config files
	createDummyConfigFile(t, appConfigPath, `
log_level: "info"
database:
  host: "localhost"
  user: "user_from_file"`)
	createDummyConfigFile(t, tradeConfigPath, `
pair: "btc_jpy"
`)

	cfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	require.NoError(t, err)

	// Assert that environment variables took precedence or supplemented the config
	assert.Equal(t, "debug", cfg.App.LogLevel, "LOG_LEVEL should be overridden by env var")
	assert.Equal(t, "db.from.env", cfg.App.Database.Host, "DB_HOST should be overridden by env var")
	assert.Equal(t, "user_from_env", cfg.App.Database.User, "DB_USER should be overridden by env var")
	assert.Equal(t, "key_from_env", cfg.APIKey, "COINCHECK_API_KEY should be supplemented by env var")
	assert.Equal(t, "", cfg.App.Database.Password, "DB_PASSWORD should be empty as it was not in file or env")
	assert.Equal(t, "btc_jpy", cfg.Trade.Pair, "Pair should be loaded from trade config")
}

// TestLoadConfig_MissingTradeConfigShouldUseSafeDefaults tests that safe defaults are used when trade config is missing.
func TestLoadConfig_MissingTradeConfigShouldUseSafeDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	appConfigPath := filepath.Join(tmpDir, "app_config.yaml")
	tradeConfigPath := filepath.Join(tmpDir, "trade_config_that_does_not_exist.yaml")

	// Create a valid app config file
	createTestAppConfigFile(appConfigPath, "info")

	// Attempt to load config with a non-existent trade config file
	cfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	require.NoError(t, err, "Loading config should not fail even if trade config is missing")
	require.NotNil(t, cfg, "Config object should not be nil")

	// Assert that the trade config has been populated with safe default values
	assert.Equal(t, 1e9, cfg.Trade.Signal.CompositeThreshold, "CompositeThreshold should be the safe default value")
	assert.Equal(t, 0.0, cfg.Trade.OrderAmount, "OrderAmount should be 0")
	assert.Equal(t, "btc_jpy", cfg.Trade.Pair, "Pair should have a default value")
}

// TestLoadConfig_MissingAppConfigShouldUseDefaults tests that default values are used when app config is missing.
func TestLoadConfig_MissingAppConfigShouldUseDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	appConfigPath := filepath.Join(tmpDir, "app_config_that_does_not_exist.yaml")
	tradeConfigPath := filepath.Join(tmpDir, "trade_config.yaml")

	// Create a valid trade config file
	createTestTradeConfigFile(tradeConfigPath, 0.5)

	// Attempt to load config with a non-existent app config file
	cfg, err := config.LoadConfig(appConfigPath, tradeConfigPath)
	require.NoError(t, err, "Loading config should not fail even if app config is missing")
	require.NotNil(t, cfg, "Config object should not be nil")

	// Assert that the app config has been populated with default values
	assert.Equal(t, "info", cfg.App.LogLevel, "LogLevel should be the default 'info'")

	// Assert that the trade config was loaded correctly
	assert.Equal(t, 0.5, cfg.Trade.Signal.CompositeThreshold, "Trade config should still be loaded correctly")
}
