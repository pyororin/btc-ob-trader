// Package config handles application configuration.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config defines the structure for all application configuration.
type Config struct {
	Pair        string       `yaml:"pair"`
	SpreadLimit float64      `yaml:"spread_limit"`
	LotMaxRatio float64      `yaml:"lot_max_ratio"`
	Long        StrategyConf `yaml:"long"`
	Short       StrategyConf `yaml:"short"`
	Volatility  VolConf      `yaml:"volatility"`
	APIKey      string       `yaml:"-"` // Loaded from env
	APISecret   string       `yaml:"-"` // Loaded from env
	LogLevel    string       `yaml:"-"` // Loaded from env or defaults
	Database    DatabaseConfig `yaml:"database"`
	DBWriter    DBWriterConfig `yaml:"db_writer"`
}

// DatabaseConfig holds all database connection parameters.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"` // Loaded from env or yaml for non-sensitive
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"` // e.g., "disable", "require", "verify-full"
}

// DBWriterConfig holds configuration for the TimescaleDB writer service.
type DBWriterConfig struct {
	BatchSize            int  `yaml:"batch_size"`
	WriteIntervalSeconds int  `yaml:"write_interval_seconds"`
	EnableAsync          bool `yaml:"enable_async"`
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

	// Load DatabaseConfig from environment variables, overriding YAML if set
	if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		cfg.Database.Host = dbHost
	}
	// Note: os.Getenv returns string, so Port needs conversion if loaded from env
	// For simplicity, assuming Port is primarily set via YAML or has a sensible default.
	// If direct env var for port is needed, add string parsing to int.
	if dbUser := os.Getenv("DB_USER"); dbUser != "" {
		cfg.Database.User = dbUser
	}
	if dbPassword := os.Getenv("DB_PASSWORD"); dbPassword != "" {
		cfg.Database.Password = dbPassword
	}
	if dbName := os.Getenv("DB_NAME"); dbName != "" {
		cfg.Database.Name = dbName
	}
	if dbSSLMode := os.Getenv("DB_SSLMODE"); dbSSLMode != "" {
		cfg.Database.SSLMode = dbSSLMode
	}
	// DBPort from environment variable requires string to int conversion.
	// Example:
	// if dbPortStr := os.Getenv("DB_PORT"); dbPortStr != "" {
	// 	if port, err := strconv.Atoi(dbPortStr); err == nil {
	// 		cfg.Database.Port = port
	// 	} else {
	// 		// Handle error: log it or return an error
	// 	}
	// }


	// TODO: Add validation for critical config values, especially for DatabaseConfig
	// e.g., ensure Host, User, Name are not empty if DB is enabled.
	// Ensure DBWriterConfig values are sensible (e.g. BatchSize > 0)
	return cfg, nil
}
