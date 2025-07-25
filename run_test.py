import json
import time
import os
from pathlib import Path
from optimizer.optimizer import main as optimizer_main, stop_go_sim_server

# --- Configuration ---
APP_ROOT = Path('/app')
PARAMS_DIR = APP_ROOT / 'data' / 'params'
JOB_FILE = PARAMS_DIR / 'optimization_job.json'

def create_job_file():
    """Creates a dummy job file to trigger the optimizer."""
    if not PARAMS_DIR.exists():
        PARAMS_DIR.mkdir(parents=True)

    job_data = {
        "trigger_type": "manual_test",
        "window_is_hours": 4, # Use a small window for faster testing
        "window_oos_hours": 1,
        "timestamp": int(time.time())
    }
    with open(JOB_FILE, 'w') as f:
        json.dump(job_data, f)
    print(f"Created job file at {JOB_FILE}")

def ensure_dummy_trade_config():
    """Creates a dummy trade config from the template if it doesn't exist."""
    from optimizer.optimizer import CONFIG_TEMPLATE_PATH, BEST_CONFIG_OUTPUT_PATH
    from jinja2 import Template

    if not BEST_CONFIG_OUTPUT_PATH.exists():
        print(f"Creating dummy trade config at {BEST_CONFIG_OUTPUT_PATH}")
        if not CONFIG_TEMPLATE_PATH.exists():
            print(f"ERROR: Config template not found at {CONFIG_TEMPLATE_PATH}")
            return

        with open(CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())

        # Use some default dummy params
        dummy_params = {
            'spread_limit': 100, 'lot_max_ratio': 0.1, 'order_ratio': 0.1,
            'adaptive_position_sizing_enabled': False, 'adaptive_num_trades': 10,
            'adaptive_reduction_step': 0.8, 'adaptive_min_ratio': 0.5,
            'long_obi_threshold': 1.0, 'long_tp': 100, 'long_sl': -100,
            'short_obi_threshold': -1.0, 'short_tp': 100, 'short_sl': -100,
            'hold_duration_ms': 500, 'slope_filter_enabled': False,
            'slope_period': 10, 'slope_threshold': 0.1, 'ewma_lambda': 0.1,
            'dynamic_obi_enabled': False, 'volatility_factor': 2.0,
            'min_threshold_factor': 0.8, 'max_threshold_factor': 1.5,
            'twap_enabled': False, 'twap_max_order_size_btc': 0.05,
            'twap_interval_seconds': 5, 'twap_partial_exit_enabled': False,
            'twap_profit_threshold': 1.0, 'twap_exit_ratio': 0.5,
            'risk_max_drawdown_percent': 20, 'risk_max_position_ratio': 0.7
        }
        config_str = template.render(dummy_params)
        with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
            f.write(config_str)

def run():
    """Runs the full optimization process for testing."""
    # Set DB_HOST to localhost for direct connection from the script
    os.environ['DB_HOST'] = 'localhost'
    print("Set DB_HOST to localhost for testing.")

    # Ensure .env file exists
    if not (APP_ROOT / '.env').exists():
        if (APP_ROOT / '.env.sample').exists():
            import shutil
            shutil.copy(APP_ROOT / '.env.sample', APP_ROOT / '.env')
            print("Copied .env.sample to .env")
        else:
            print("Error: .env.sample not found.")
            return

    ensure_dummy_trade_config()
    create_job_file()

    try:
        print("Starting optimizer...")
        # We need to run the optimizer in a way that we can capture its logs
        # and it stops after one job. The current optimizer loops forever.
        # For this test, we will trust the logs from the optimizer's main function.
        optimizer_main(run_once=True)
    except Exception as e:
        print(f"An error occurred during optimization: {e}")
    finally:
        print("Stopping Go server from test script...")
        stop_go_sim_server()
        if JOB_FILE.exists():
            os.remove(JOB_FILE)
            print("Cleaned up job file.")

if __name__ == "__main__":
    run()
