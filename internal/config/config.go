// Package config handles application configuration.
package config

import (
	"os"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// globalConfig holds the current configuration, accessible atomically.
var globalConfig atomic.Value

// AppConfig defines the structure for application-level configuration.
type AppConfig struct {
	LogLevel  string          `yaml:"log_level"`
	Order     OrderConfig     `yaml:"order"`
	Database  DatabaseConfig  `yaml:"database"`
	DBWriter  DBWriterConfig  `yaml:"db_writer"`
	Replay    ReplayConfig    `yaml:"replay"`
	PnlReport PnlReportConfig `yaml:"pnl_report"`
	Alert     AlertConfig     `yaml:"alert"`
}

// TradeConfig defines the structure for trading strategy configuration.
type TradeConfig struct {
	Pair                   string                 `yaml:"pair"`
	SpreadLimit            float64                `yaml:"spread_limit"`
	LotMaxRatio            float64                `yaml:"lot_max_ratio"`
	OrderRatio             float64                `yaml:"order_ratio"`
	Long                   StrategyConf           `yaml:"long"`
	Short                  StrategyConf           `yaml:"short"`
	Volatility             VolConf                `yaml:"volatility"`
	Twap                   TwapConfig             `yaml:"twap"`
	Signal                 SignalConfig           `yaml:"signal"`
	Risk                   RiskConfig             `yaml:"risk"`
	AdaptivePositionSizing AdaptiveSizingConfig `yaml:"adaptive_position_sizing"`
}

// Config wraps both application and trading configurations.
type Config struct {
	App   AppConfig
	Trade TradeConfig
	APIKey    string `yaml:"-"` // Loaded from env
	APISecret string `yaml:"-"` // Loaded from env
}

// AdaptiveSizingConfig holds settings for the adaptive position sizing feature.
type AdaptiveSizingConfig struct {
	Enabled       bool    `yaml:"enabled"`
	NumTrades     int     `yaml:"num_trades"`
	ReductionStep float64 `yaml:"reduction_step"`
	MinRatio      float64 `yaml:"min_ratio"`
}

// AlertConfig holds alert settings.
type AlertConfig struct {
	Discord DiscordConfig `yaml:"discord"`
}

// DiscordConfig holds Discord notification settings.
type DiscordConfig struct {
	BotToken string `yaml:"bot_token"`
	UserID   string `yaml:"user_id"`
}

// RiskConfig holds risk management settings.
type RiskConfig struct {
	MaxDrawdownPercent float64 `yaml:"max_drawdown_percent"`
	MaxPositionJPY     float64 `yaml:"max_position_jpy"`
}

// SignalConfig holds configuration for signal generation.
type SignalConfig struct {
	HoldDurationMs int             `yaml:"hold_duration_ms"`
	SlopeFilter    SlopeFilterConfig `yaml:"slope_filter"`
}

// SlopeFilterConfig holds settings for the OBI slope filter.
type SlopeFilterConfig struct {
	Enabled   bool    `yaml:"enabled"`
	Period    int     `yaml:"period"`
	Threshold float64 `yaml:"threshold"`
}

// OrderConfig holds configuration for the execution engine.
type OrderConfig struct {
	TimeoutSeconds  int `yaml:"timeout_seconds"`
	PollIntervalMs int `yaml:"poll_interval_ms"`
}

// TwapConfig holds configuration for the TWAP execution strategy.
type TwapConfig struct {
	Enabled             bool    `yaml:"enabled"`
	MaxOrderSizeBtc     float64 `yaml:"max_order_size_btc"`
	IntervalSeconds     int     `yaml:"interval_seconds"`
	PartialExitEnabled  bool    `yaml:"partial_exit_enabled"`
	ProfitThreshold     float64 `yaml:"profit_threshold"`
	ExitRatio           float64 `yaml:"exit_ratio"`
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

// loadFromFiles loads configuration from app and trade YAML files and environment variables.
func loadFromFiles(appConfigPath, tradeConfigPath string) (*Config, error) {
	// Load app config
	appCfg := AppConfig{
		LogLevel: "info",
	}
	appFile, err := os.ReadFile(appConfigPath)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(appFile, &appCfg); err != nil {
		return nil, err
	}

	// Load trade config
	var tradeCfg TradeConfig
	tradeFile, err := os.ReadFile(tradeConfigPath)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(tradeFile, &tradeCfg); err != nil {
		return nil, err
	}

	cfg := &Config{
		App:   appCfg,
		Trade: tradeCfg,
	}

	// Preserve API keys from the currently active config if they are not re-set by env vars
	currentCfg := GetConfig()
	if currentCfg != nil {
		cfg.APIKey = currentCfg.APIKey
		cfg.APISecret = currentCfg.APISecret
	}

	if apiKey := os.Getenv("COINCHECK_API_KEY"); apiKey != "" {
		cfg.APIKey = apiKey
	}
	if apiSecret := os.Getenv("COINCHECK_API_SECRET"); apiSecret != "" {
		cfg.APISecret = apiSecret
	}
	if logLevel := os.Getenv("LOG_LEVEL"); logLevel != "" {
		cfg.App.LogLevel = logLevel
	}
	if dbHost := os.Getenv("DB_HOST"); dbHost != "" {
		cfg.App.Database.Host = dbHost
	}
	if dbUser := os.Getenv("DB_USER"); dbUser != "" {
		cfg.App.Database.User = dbUser
	}
	if dbPassword := os.Getenv("DB_PASSWORD"); dbPassword != "" {
		cfg.App.Database.Password = dbPassword
	}
	if dbName := os.Getenv("DB_NAME"); dbName != "" {
		cfg.App.Database.Name = dbName
	}
	if dbSSLMode := os.Getenv("DB_SSLMODE"); dbSSLMode != "" {
		cfg.App.Database.SSLMode = dbSSLMode
	}
	if discordBotToken := os.Getenv("DISCORD_BOT_TOKEN"); discordBotToken != "" {
		cfg.App.Alert.Discord.BotToken = discordBotToken
	}
	if discordUserID := os.Getenv("DISCORD_USER_ID"); discordUserID != "" {
		cfg.App.Alert.Discord.UserID = discordUserID
	}

	return cfg, nil
}

// LoadConfig loads the configuration for the first time and stores it globally.
func LoadConfig(appConfigPath, tradeConfigPath string) (*Config, error) {
	cfg, err := loadFromFiles(appConfigPath, tradeConfigPath)
	if err != nil {
		return nil, err
	}
	globalConfig.Store(cfg)
	return cfg, nil
}

// ReloadConfig reloads the configuration from the given path and atomically
// updates the global configuration.
func ReloadConfig(appConfigPath, tradeConfigPath string) (*Config, error) {
	cfg, err := loadFromFiles(appConfigPath, tradeConfigPath)
	if err != nil {
		return nil, err
	}
	globalConfig.Store(cfg)
	return cfg, nil
}

// GetConfig returns the current global configuration.
func GetConfig() *Config {
	cfg, _ := globalConfig.Load().(*Config)
	return cfg
}
