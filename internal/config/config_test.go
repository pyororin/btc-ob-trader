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

// TestMain sets up and tears down the test environment.
func TestMain(m *testing.M) {
	// Setup: Create a temporary directory for config files
	tmpDir, err := os.MkdirTemp("", "config_test")
	if err != nil {
		panic("failed to create temp dir for config test")
	}

	// Create initial config file
	createTestConfigFile(filepath.Join(tmpDir, "config.yaml"), 10.0)

	// Run tests
	code := m.Run()

	// Teardown: Remove the temporary directory
	os.RemoveAll(tmpDir)

	os.Exit(code)
}

// createTestConfigFile creates a dummy config file for testing.
func createTestConfigFile(path string, obiThreshold float64) {
	yamlContent := fmt.Sprintf(`
pair: "btc_jpy"
log_level: "info"
long:
  obi_threshold: %.2f
`, obiThreshold)
	err := os.WriteFile(path, []byte(yamlContent), 0644)
	if err != nil {
		panic(err)
	}
}

// TestConfigReloading tests the dynamic reloading of configuration.
func TestConfigReloading(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// 1. Create and load initial config
	createTestConfigFile(configPath, 10.0)
	initialCfg, err := config.LoadConfig(configPath)
	require.NoError(t, err, "Initial config loading should succeed")
	require.Equal(t, 10.0, initialCfg.Long.OBIThreshold, "Initial OBI threshold should be 10.0")

	// 2. Concurrently access config while reloading
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond) // Give some time for the main thread to start reading
		t.Log("Goroutine: Reloading config...")
		createTestConfigFile(configPath, 20.0) // Update the config file
		_, err := config.ReloadConfig(configPath)
		assert.NoError(t, err, "Config reloading should succeed")
		t.Log("Goroutine: Config reloaded.")
	}()

	// Continuously read the config to check for race conditions and see the update
	var finalOBI float64
	for i := 0; i < 100; i++ {
		currentCfg := config.GetConfig()
		if currentCfg.Long.OBIThreshold == 20.0 {
			finalOBI = currentCfg.Long.OBIThreshold
			break
		}
		time.Sleep(10 * time.Millisecond) // Simulate work
	}
	wg.Wait() // Wait for the reloading goroutine to finish

	// 3. Verify the config was updated
	assert.Equal(t, 20.0, finalOBI, "OBI threshold should have been updated to 20.0")

	finalCfg := config.GetConfig()
	assert.Equal(t, 20.0, finalCfg.Long.OBIThreshold, "Final config check should show updated OBI threshold")
}

// Helper function to create a dummy config file with specific content
func createDummyConfigFile(t *testing.T, path, content string) {
	t.Helper()
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

// TestLoadConfig_EnvVarOverride tests if environment variables correctly override yaml values.
func TestLoadConfig_EnvVarOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Initial config file
	createDummyConfigFile(t, configPath, `
log_level: "info"
database:
  host: "localhost"
  user: "user_from_file"`)

	// Set environment variables to override and supplement the config file
	t.Setenv("LOG_LEVEL", "debug") // Override
	t.Setenv("DB_HOST", "db.from.env") // Override
	t.Setenv("DB_USER", "user_from_env") // Override
	t.Setenv("COINCHECK_API_KEY", "key_from_env") // Supplement

	// Unset a variable to ensure it doesn't carry over from the host environment
	// Note: This is good practice but might not be strictly necessary if the test runner isolates env vars.
	os.Unsetenv("DB_PASSWORD")

	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err)

	// Assert that environment variables took precedence or supplemented the config
	assert.Equal(t, "debug", cfg.LogLevel, "LOG_LEVEL should be overridden by env var")
	assert.Equal(t, "db.from.env", cfg.Database.Host, "DB_HOST should be overridden by env var")
	assert.Equal(t, "user_from_env", cfg.Database.User, "DB_USER should be overridden by env var")
	assert.Equal(t, "key_from_env", cfg.APIKey, "COINCHECK_API_KEY should be supplemented by env var")
	assert.Equal(t, "", cfg.Database.Password, "DB_PASSWORD should be empty as it was not in file or env")
}
