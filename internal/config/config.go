// Package config handles application configuration.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config defines the structure for all application configuration.
type Config struct {
	Pair        string         `yaml:"pair"`
	SpreadLimit float64        `yaml:"spread_limit"`
	LotMaxRatio float64        `yaml:"lot_max_ratio"`
	OrderRatio  float64        `yaml:"order_ratio"`
	Long        StrategyConf   `yaml:"long"`
	Short       StrategyConf   `yaml:"short"`
	Volatility  VolConf        `yaml:"volatility"`
	Twap        TwapConfig     `yaml:"twap"`
	Signal      SignalConfig   `yaml:"signal"`
	APIKey      string         `yaml:"-"` // Loaded from env
	APISecret   string         `yaml:"-"` // Loaded from env
	LogLevel    string         `yaml:"log_level"`
	Order       OrderConfig    `yaml:"order"`
	Database    DatabaseConfig  `yaml:"database"`
	DBWriter    DBWriterConfig  `yaml:"db_writer"`
	Replay      ReplayConfig    `yaml:"replay"`
	PnlReport   PnlReportConfig `yaml:"pnl_report"`
	Risk        RiskConfig      `yaml:"risk"`
}

// RiskConfig holds risk management settings.
type RiskConfig struct {
	MaxDrawdownPercent float64 `yaml:"max_drawdown_percent"`
	MaxPositionJPY     float64 `yaml:"max_position_jpy"`
}

// SignalConfig holds configuration for signal generation.
type SignalConfig struct {
	HoldDurationMs int `yaml:"hold_duration_ms"`
}

// OrderConfig holds configuration for the execution engine.
type OrderConfig struct {
	TimeoutSeconds  int `yaml:"timeout_seconds"`
	PollIntervalMs int `yaml:"poll_interval_ms"`
}

// TwapConfig holds configuration for the TWAP execution strategy.
type TwapConfig struct {
	Enabled         bool    `yaml:"enabled"`
	MaxOrderSizeBtc float64 `yaml:"max_order_size_btc"`
	IntervalSeconds int     `yaml:"interval_seconds"`
}

// ReplayConfig holds configuration for the replay mode.
type ReplayConfig struct {
	StartTime string `yaml:"start_time"`
	EndTime   string `yaml:"end_time"`
}

// PnlReportConfig holds settings for PnL reporting.
type PnlReportConfig struct {
	IntervalMinutes int `yaml:"interval_minutes"`
	MaxAgeHours     int `yaml:"max_age_hours"`
}

// DatabaseConfig holds all database connection parameters.
type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Name     string `yaml:"name"`
	SSLMode  string `yaml:"sslmode"`
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

// VolConf holds configuration for volatility calculation and dynamic adjustments.
type VolConf struct {
	EWMALambda   float64      `yaml:"ewma_lambda"`
	DynamicOBI   DynamicOBIConf `yaml:"dynamic_obi"`
}

// DynamicOBIConf holds configuration for dynamic OBI threshold adjustments.
type DynamicOBIConf struct {
	Enabled          bool    `yaml:"enabled"`
	VolatilityFactor float64 `yaml:"volatility_factor"`
	MinThresholdFactor float64 `yaml:"min_threshold_factor"`
	MaxThresholdFactor float64 `yaml:"max_threshold_factor"`
}

// LoadConfig loads configuration from the specified YAML file path
// and environment variables.
func LoadConfig(configPath string) (*Config, error) {
	cfg := &Config{
		LogLevel: "info",
	}

	file, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, cfg)
	if err != nil {
		return nil, err
	}

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
		cfg.Database.Host = dbHost
	}
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

	return cfg, nil
}
