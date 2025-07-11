// Package config handles application configuration.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config defines the structure for all application configuration.
type Config struct {
	Pair         string       `yaml:"pair"`
	SpreadLimit  float64      `yaml:"spread_limit"`
	LotMaxRatio  float64      `yaml:"lot_max_ratio"`
	Long         StrategyConf `yaml:"long"`
	Short        StrategyConf `yaml:"short"`
	Volatility   VolConf      `yaml:"volatility"`
	APIKey       string       `yaml:"-"` // Loaded from env
	APISecret    string       `yaml:"-"` // Loaded from env
	LogLevel     string       `yaml:"-"` // Loaded from env or defaults
	DBHost       string       `yaml:"-"`
	DBPort       string       `yaml:"-"`
	DBUser       string       `yaml:"-"`
	DBPassword   string       `yaml:"-"`
	DBName       string       `yaml:"-"`
}

// StrategyConf holds configuration for long/short strategies.
type StrategyConf struct {
	OBIThreshold float64 `yaml:"obi_threshold"`
	TP           float64 `yaml:"tp"` // Take Profit
	SL           float64 `yaml:"sl"` // Stop Loss
}

// VolConf holds configuration for volatility calculation.
type VolConf struct {
	EWMA Lambda `yaml:"ewma_lambda"`
}

// Lambda is a type alias for float64 for clarity in config.
type Lambda float64

// LoadConfig loads configuration from the specified YAML file path
// and environment variables.
func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{
		// Default values
		LogLevel: "info",
	}

	// Read YAML file
	file, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, cfg)
	if err != nil {
		return nil, err
	}

	// Load sensitive data and overrides from environment variables
	if apiKey := os.Getenv("COINCHECK_API_KEY"); apiKey != "" {
		cfg.APIKey = apiKey
	}
	if apiSecret := os.Getenv("COINCHECK_API_SECRET"); apiSecret != "" {
		cfg.APISecret = apiSecret
	}
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.LogLevel = logLevel
	}
	if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		cfg.DBHost = dbHost
	}
	if dbPort := os.Getenv("DB_PORT"); dbPort != "" {
		cfg.DBPort = dbPort
	}
	if dbUser := os.Getenv("DB_USER"); dbUser != "" {
		cfg.DBUser = dbUser
	}
	if dbPassword := os.Getenv("DB_PASSWORD"); dbPassword != "" {
		cfg.DBPassword = dbPassword
	}
	if dbName := os.Getenv("DB_NAME"); dbName != "" {
		cfg.DBName = dbName
	}

	// TODO: Add validation for critical config values
	return cfg, nil
}
