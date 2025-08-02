import yaml
import logging
from pathlib import Path
import os

# --- Constants ---
APP_ROOT = Path('/app')
DEFAULT_PARAMS_DIR = APP_ROOT / 'data' / 'params'
DEFAULT_STORAGE_URL = f"sqlite:///{DEFAULT_PARAMS_DIR / 'optuna_study.db'}"

def load_config():
    """
    Loads configuration from the YAML file.

    This function looks for 'optimizer_config.yaml' in the central '/app/config'
    directory. It logs an error and returns an empty dictionary if the file
    is not found or cannot be parsed.

    Returns:
        dict: A dictionary containing the configuration, or an empty dict on failure.
    """
    config_path = APP_ROOT / 'config' / 'optimizer_config.yaml'
    if not config_path.exists():
        logging.error(f"Configuration file not found at {config_path}")
        return {}
    try:
        with open(config_path, 'r') as f:
            return yaml.safe_load(f)
    except yaml.YAMLError as e:
        logging.error(f"Error parsing YAML config file: {e}")
        return {}

# --- Global Configuration Object ---
# Load the configuration once when the module is imported.
CONFIG = load_config()

# --- Derived Configuration Variables ---
# Provide easy access to frequently used config values with sensible defaults.

# Directories and Paths
PARAMS_DIR = Path(os.getenv('PARAMS_DIR', CONFIG.get('params_dir', DEFAULT_PARAMS_DIR)))
SIMULATION_DIR = APP_ROOT / 'simulation'
BIN_DIR = APP_ROOT / 'bin'
CONFIG_TEMPLATE_PATH = PARAMS_DIR / 'trade_config.yaml.template'
BEST_CONFIG_OUTPUT_PATH = PARAMS_DIR / 'trade_config.yaml'
JOB_FILE = PARAMS_DIR / 'optimization_job.json'
SIMULATION_BINARY_PATH = BIN_DIR / 'bot'

# Database URLs
STORAGE_URL = os.getenv('STORAGE_URL', CONFIG.get('storage_url', DEFAULT_STORAGE_URL))

# Optimizer Settings
N_TRIALS = CONFIG.get('n_trials', 800)
WARM_START_MAX_TRIALS = CONFIG.get('warm_start_max_trials', 200)
MIN_TRADES_FOR_PRUNING = CONFIG.get('min_trades_for_pruning', 5)
MAX_RETRY = CONFIG.get('max_retry', 5)
EARLY_STOP_COUNT = CONFIG.get('early_stop_count', 3)
EARLY_STOP_THRESHOLD_RATIO = CONFIG.get('early_stop_threshold_ratio', -0.5)

# Out-of-Sample (OOS) Validation Criteria
OOS_MIN_SHARPE_RATIO = CONFIG.get('oos_min_sharpe_ratio', 0.5)
OOS_MIN_TRADES = CONFIG.get('oos_min_trades', 10)

# Objective Function Weights
OBJECTIVE_WEIGHTS = CONFIG.get('objective_weights', {
    'sharpe_ratio': 1.5,
    'profit_factor': 1.0,
    'relative_drawdown': 1.0,
    'sqn': 0.5,
    'trades': 0.5
})

# Analyzer Settings
ANALYZER_CONFIG = CONFIG.get('analyzer', {})
TOP_TRIALS_QUANTILE = ANALYZER_CONFIG.get('top_trials_quantile', 0.1)
MIN_TRIALS_FOR_ANALYSIS = ANALYZER_CONFIG.get('min_trials_for_analysis', 10)

# Drift Monitor Settings
DB_USER = os.getenv('DB_USER')
DB_PASSWORD = os.getenv('DB_PASSWORD')
DB_NAME = os.getenv('DB_NAME')
DB_HOST = os.getenv('DB_HOST', 'timescaledb')
DB_PORT = os.getenv('DB_PORT', '5432')
CHECK_INTERVAL_SECONDS = int(os.getenv('CHECK_INTERVAL_SECONDS', CONFIG.get('check_interval_seconds', 300)))
SHARPE_DRIFT_THRESHOLD_SD = CONFIG.get('sharpe_drift_threshold_sd', -0.5)
PF_DRIFT_THRESHOLD = CONFIG.get('pf_drift_threshold', 0.9)
SHARPE_EMERGENCY_THRESHOLD_SD = CONFIG.get('sharpe_emergency_threshold_sd', -1.0)
