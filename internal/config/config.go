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
	LogLevel                   string          `yaml:"log_level"`
	ObiCalculatorChannelSize   int             `yaml:"obi_calculator_channel_size"`
	SignalEvaluationIntervalMs int             `yaml:"signal_evaluation_interval_ms"`
	Order                      OrderConfig     `yaml:"order"`
	Database                   DatabaseConfig  `yaml:"database"`
	DBWriter                   DBWriterConfig  `yaml:"db_writer"`
	Replay                     ReplayConfig    `yaml:"replay"`
	PnlReport                  PnlReportConfig `yaml:"pnl_report"`
	Alert                      AlertConfig     `yaml:"alert"`
}

// TradeConfig defines the structure for trading strategy configuration.
type TradeConfig struct {
	Pair                   string                 `yaml:"pair"`
	OrderAmount            float64                `yaml:"order_amount"`
	EntryPriceOffset       float64                `yaml:"entry_price_offset"`
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
	App         AppConfig
	Trade       *TradeConfig
	EnableTrade bool   `yaml:"-"` // Loaded from env
	APIKey      string `yaml:"-"` // Loaded from env
	APISecret   string `yaml:"-"` // Loaded from env
}

// AdaptiveSizingConfig holds settings for the adaptive position sizing feature.
type AdaptiveSizingConfig struct {
	Enabled       FlexBool `yaml:"enabled"`
	NumTrades     int      `yaml:"num_trades"`
	ReductionStep float64  `yaml:"reduction_step"`
	MinRatio      float64  `yaml:"min_ratio"`
}

// AlertConfig holds alert settings.
type AlertConfig struct {
	Enabled FlexBool `yaml:"enabled"`
}

// RiskConfig holds risk management settings.
type RiskConfig struct {
	MaxDrawdownPercent float64 `yaml:"max_drawdown_percent"`
	MaxPositionRatio   float64 `yaml:"max_position_ratio"`
}

// SignalConfig holds configuration for signal generation.
type SignalConfig struct {
	HoldDurationMs     int               `yaml:"hold_duration_ms"`
	SlopeFilter        SlopeFilterConfig `yaml:"slope_filter"`
	CVDWindowMinutes   int               `yaml:"cvd_window_minutes"`
	OBIWeight          float64           `yaml:"obi_weight"`
	OFIWeight          float64           `yaml:"ofi_weight"`
	CVDWeight          float64           `yaml:"cvd_weight"`
	MicroPriceWeight   float64           `yaml:"micro_price_weight"`
	CompositeThreshold float64           `yaml:"composite_threshold"`
}

// SlopeFilterConfig holds settings for the OBI slope filter.
type SlopeFilterConfig struct {
	Enabled   FlexBool `yaml:"enabled"`
	Period    int      `yaml:"period"`
	Threshold float64  `yaml:"threshold"`
}

// OrderConfig holds configuration for the execution engine.
type OrderConfig struct {
	TimeoutSeconds  int `yaml:"timeout_seconds"`
	PollIntervalMs int `yaml:"poll_interval_ms"`
}

// TwapConfig holds configuration for the TWAP execution strategy.
type TwapConfig struct {
	Enabled             FlexBool `yaml:"enabled"`
	MaxOrderSizeBtc     float64  `yaml:"max_order_size_btc"`
	IntervalSeconds     int      `yaml:"interval_seconds"`
	PartialExitEnabled  FlexBool `yaml:"partial_exit_enabled"`
	ProfitThreshold     float64  `yaml:"profit_threshold"`
	ExitRatio           float64  `yaml:"exit_ratio"`
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
	BatchSize            int      `yaml:"batch_size"`
	WriteIntervalSeconds int      `yaml:"write_interval_seconds"`
	EnableAsync          FlexBool `yaml:"enable_async"`
}

// StrategyConf holds configuration for long/short strategies.
type StrategyConf struct {
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
	Enabled          FlexBool `yaml:"enabled"`
	VolatilityFactor float64  `yaml:"volatility_factor"`
	MinThresholdFactor float64 `yaml:"min_threshold_factor"`
	MaxThresholdFactor float64 `yaml:"max_threshold_factor"`
}

// loadFromFiles loads configuration from app and trade YAML files and environment variables.
func loadFromFiles(appConfigPath, tradeConfigPath string) (*Config, error) {
	var tradeCfgBytes []byte
	var err error

	if tradeConfigPath != "" {
		tradeCfgBytes, err = os.ReadFile(tradeConfigPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, err // File exists but couldn't be read
			}
			// File does not exist, which is acceptable. tradeCfgBytes remains nil.
		}
	}
	return loadConfigFromBytes(appConfigPath, tradeCfgBytes)
}

// loadConfigFromBytes loads app config from a file and trade config from a byte slice.
func loadConfigFromBytes(appConfigPath string, tradeConfigBytes []byte) (*Config, error) {
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

	// Load trade config from bytes (optional)
	var tradeCfg *TradeConfig
	if len(tradeConfigBytes) > 0 {
		var tc TradeConfig
		if err := yaml.Unmarshal(tradeConfigBytes, &tc); err != nil {
			return nil, err
		}
		tradeCfg = &tc
	}

	cfg := &Config{
		App:   appCfg,
		Trade: tradeCfg,
	}

	// Preserve API keys and environment variables
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
	cfg.EnableTrade = os.Getenv("ENABLE_TRADE") == "true"

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

// LoadConfigFromBytes loads config with trade config from a byte slice.
func LoadConfigFromBytes(appConfigPath string, tradeConfigBytes []byte) (*Config, error) {
	cfg, err := loadConfigFromBytes(appConfigPath, tradeConfigBytes)
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

// GetConfigCopy returns a deep copy of the current configuration.
// This is crucial for running simulations in parallel without race conditions.
func GetConfigCopy() *Config {
	currentCfg := GetConfig()
	if currentCfg == nil {
		return nil
	}

	// Create a new Config struct for the copy.
	// AppConfig is treated as mostly static, so a shallow copy is acceptable.
	// If AppConfig had pointers or slices that could be modified, a deep copy would be needed.
	cfgCopy := &Config{
		App:         currentCfg.App,
		EnableTrade: currentCfg.EnableTrade,
		APIKey:      currentCfg.APIKey,
		APISecret:   currentCfg.APISecret,
	}

	// Deep copy the TradeConfig if it exists
	if currentCfg.Trade != nil {
		tradeCopy := *currentCfg.Trade
		cfgCopy.Trade = &tradeCopy
	}

	return cfgCopy
}

// LoadTradeConfigFromBytes unmarshals a TradeConfig from a byte slice.
func LoadTradeConfigFromBytes(data []byte) (*TradeConfig, error) {
	var tradeCfg TradeConfig
	if err := yaml.Unmarshal(data, &tradeCfg); err != nil {
		return nil, err
	}
	return &tradeCfg, nil
}

// SetTestConfig sets the global configuration to a specific config object.
// This is intended for use in tests to inject a mock configuration.
func SetTestConfig(cfg *Config) {
	globalConfig.Store(cfg)
}
