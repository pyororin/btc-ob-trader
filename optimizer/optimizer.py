import yaml
import optuna
from optuna.pruners import HyperbandPruner
import subprocess
import os
import json
import tempfile
import logging
import time
from jinja2 import Template
from pathlib import Path
import shutil

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)

# --- Environment & Constants ---
APP_ROOT = Path('/app')
PARAMS_DIR = Path(os.getenv('PARAMS_DIR', '/data/params'))
JOB_FILE = PARAMS_DIR / 'optimization_job.json'
SIMULATION_DIR = APP_ROOT / 'simulation'
CONFIG_TEMPLATE_PATH = APP_ROOT / 'config/trade_config.yaml.template'
BEST_CONFIG_OUTPUT_PATH = PARAMS_DIR / 'trade_config.yaml'
N_TRIALS = int(os.getenv('N_TRIALS', '100'))
STORAGE_URL = os.getenv('STORAGE_URL', f"sqlite:///{PARAMS_DIR / 'optuna_study.db'}")

# --- Pass/Fail Criteria ---
OOS_MIN_PROFIT_FACTOR = 1.2
OOS_MIN_SHARPE_RATIO = 0.5
# OOS_MAX_DRAWDOWN_PERCENTILE = 0.85 # This is complex and needs historical data. Skipped for now.

# --- Sample Size Guard ---
MIN_EXECUTED_TRADES = 5000
MIN_ORDER_BOOK_SNAPSHOTS = 100000

# Global variable to hold the path to the current simulation data
# This is a simple way to pass the data path to the objective function.
CURRENT_SIM_CSV_PATH = None

def export_data(hours_before, is_oos_split=False, oos_hours=0):
    """
    Exports data from the database using the make command.
    Returns the path to the exported CSV file.
    """
    logging.info(f"Exporting data for the last {hours_before} hours...")

    # Clean previous simulation data
    if SIMULATION_DIR.exists():
        shutil.rmtree(SIMULATION_DIR)
    SIMULATION_DIR.mkdir()

    # Base command
    db_user = os.getenv('DB_USER')
    db_password = os.getenv('DB_PASSWORD')
    db_name = os.getenv('DB_NAME')
    db_host = os.getenv('DB_HOST')

    cmd = [
        'go',
        'run',
        'cmd/export/main.go',
        f'--hours-before={hours_before}',
        '--no-zip'
    ]


    try:
        subprocess.run(cmd, check=True, capture_output=True, text=True, cwd=APP_ROOT)

        # Find the exported CSV file
        exported_files = list(SIMULATION_DIR.glob("*.csv"))
        if not exported_files:
            logging.error("No CSV file found after export.")
            return None, None

        # Sort by modification time to get the latest
        exported_files.sort(key=os.path.getmtime, reverse=True)
        full_dataset_path = exported_files[0]
        logging.info(f"Successfully exported data to {full_dataset_path}")

        if not is_oos_split:
            return full_dataset_path, None

        # Split into In-Sample (IS) and Out-of-Sample (OOS)
        # This is a simplified split. A more robust implementation would use pandas.
        is_ratio = (hours_before - oos_hours) / hours_before

        is_path = SIMULATION_DIR / "is_data.csv"
        oos_path = SIMULATION_DIR / "oos_data.csv"

        with open(full_dataset_path, 'r') as f_full, open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
            lines = f_full.readlines()
            header = lines[0]
            data_lines = lines[1:]
            split_index = int(len(data_lines) * is_ratio)

            f_is.write(header)
            f_is.writelines(data_lines[:split_index])

            f_oos.write(header)
            f_oos.writelines(data_lines[split_index:])

        logging.info(f"Split data into IS ({is_path}) and OOS ({oos_path})")
        return is_path, oos_path

    except subprocess.CalledProcessError as e:
        logging.error(f"Failed to export data: {e.stderr}")
        return None, None

def run_simulation(trade_config_path, sim_csv_path):
    """Runs the Go simulation and returns the JSON summary."""
    command = [
        str(APP_ROOT / 'build' / 'obi-scalp-bot'),
        '--simulate',
        f'--csv={sim_csv_path}',
        f'--trade-config={trade_config_path}',
        '--json-output'
    ]
    try:
        # We need to run the simulation from the root directory for it to find all necessary files
        result = subprocess.run(command, capture_output=True, text=True, check=True, cwd=APP_ROOT)
        return json.loads(result.stdout)
    except (subprocess.CalledProcessError, json.JSONDecodeError) as e:
        logging.error(f"Simulation failed: {e.stdout} {e.stderr}")
        return None

def objective(trial):
    """Optuna objective function."""
    global CURRENT_SIM_CSV_PATH

    params = {
        'spread_limit': trial.suggest_int('spread_limit', 10, 150),
        'lot_max_ratio': trial.suggest_float('lot_max_ratio', 0.01, 0.2),
        'order_ratio': trial.suggest_float('order_ratio', 0.05, 0.25),
        'long_tp': trial.suggest_int('long_tp', 50, 500),
        'long_sl': trial.suggest_int('long_sl', -500, -50),
        'short_tp': trial.suggest_int('short_tp', 50, 500),
        'short_sl': trial.suggest_int('short_sl', -500, -50),
    }

    with open(CONFIG_TEMPLATE_PATH, 'r') as f:
        template = Template(f.read())

    rendered_config_str = template.render(params)

    with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml') as temp_config_file:
        temp_config_file.write(rendered_config_str)
        temp_config_path = temp_config_file.name

    summary = run_simulation(temp_config_path, CURRENT_SIM_CSV_PATH)
    os.remove(temp_config_path)

    if summary is None:
        return 0.0

    # The metric to optimize
    profit_factor = summary.get('ProfitFactor', 0.0)
    return profit_factor


def ensure_default_config_exists():
    """Checks if a trade config exists, and creates a default one if not."""
    if not BEST_CONFIG_OUTPUT_PATH.exists():
        logging.info(f"{BEST_CONFIG_OUTPUT_PATH} not found. Creating a default config from template.")
        try:
            # Ensure the parent directory exists
            PARAMS_DIR.mkdir(parents=True, exist_ok=True)

            with open(CONFIG_TEMPLATE_PATH, 'r') as f:
                template = Template(f.read())

            # Use default values from the template (or define simple defaults here)
            # This part might need adjustment if the template requires specific variables
            default_params = {
                # Basic Trading
                "pair": "btc_jpy",
                "spread_limit": 100,
                # Position Sizing
                "lot_max_ratio": 0.1,
                "order_ratio": 0.1,
                "adaptive_position_sizing_enabled": False,
                "adaptive_num_trades": 10,
                "adaptive_reduction_step": 0.8,
                "adaptive_min_ratio": 0.5,
                # Entry Strategy
                "long_obi_threshold": 1.0,
                "long_tp": 150,
                "long_sl": -150,
                "short_obi_threshold": -1.0,
                "short_tp": 150,
                "short_sl": -150,
                # Signal Filters
                "hold_duration_ms": 500,
                "slope_filter_enabled": False,
                "slope_period": 10,
                "slope_threshold": 0.1,
                # Dynamic Parameters
                "ewma_lambda": 0.1,
                "dynamic_obi_enabled": False,
                "volatility_factor": 1.0,
                "min_threshold_factor": 0.8,
                "max_threshold_factor": 1.5,
                # Execution Strategy
                "twap_enabled": False,
                "twap_max_order_size_btc": 0.01,
                "twap_interval_seconds": 5,
                "twap_partial_exit_enabled": False,
                "twap_profit_threshold": 0.5,
                "twap_exit_ratio": 0.5,
                # Risk Management
                "risk_max_drawdown_percent": 20,
                "risk_max_position_ratio": 0.9,
            }
            default_config_str = template.render(default_params)

            with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
                f.write(default_config_str)
            logging.info(f"Default trade config created at {BEST_CONFIG_OUTPUT_PATH}")

        except Exception as e:
            logging.error(f"Could not create default config: {e}")

def main():
    """Main loop for the optimizer."""
    ensure_default_config_exists()
    logging.info("Optimizer started. Waiting for optimization job...")
    while True:
        if JOB_FILE.exists():
            logging.info(f"Found job file: {JOB_FILE}")
            with open(JOB_FILE, 'r') as f:
                try:
                    job = json.load(f)
                except json.JSONDecodeError:
                    logging.error("Invalid job file. Deleting.")
                    os.remove(JOB_FILE)
                    continue

            # --- Data Export & Validation ---
            is_hours = job['window_is_hours']
            oos_hours = job['window_oos_hours']

            is_csv_path, oos_csv_path = export_data(is_hours + oos_hours, is_oos_split=True, oos_hours=oos_hours)

            if not is_csv_path or not oos_csv_path:
                logging.error("Failed to get data. Aborting optimization run.")
                os.remove(JOB_FILE)
                continue

            # --- In-Sample Optimization ---
            global CURRENT_SIM_CSV_PATH
            CURRENT_SIM_CSV_PATH = is_csv_path

            # Remove old study DB
            if os.path.exists(STORAGE_URL.replace('sqlite:///', '')):
                os.remove(STORAGE_URL.replace('sqlite:///', ''))

            study = optuna.create_study(
                study_name='obi-scalp-optimization',
                storage=STORAGE_URL,
                direction='maximize',
                pruner=HyperbandPruner()
            )
            study.optimize(objective, n_trials=N_TRIALS, n_jobs=-1)

            best_params = study.best_trial.params
            logging.info(f"Best In-Sample Params: {best_params}")

            # --- Out-of-Sample Validation ---
            with open(CONFIG_TEMPLATE_PATH, 'r') as f:
                template = Template(f.read())
            best_config_str = template.render(best_params)

            with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml') as temp_config_file:
                temp_config_file.write(best_config_str)
                best_config_path = temp_config_file.name

            logging.info("Running Out-of-Sample validation...")
            oos_summary = run_simulation(best_config_path, oos_csv_path)
            os.remove(best_config_path)

            if oos_summary is None:
                logging.error("OOS validation failed. Discarding parameters.")
                os.remove(JOB_FILE)
                continue

            # --- Check Pass Criteria ---
            oos_pf = oos_summary.get('ProfitFactor', 0.0)
            oos_sharpe = oos_summary.get('SharpeRatio', 0.0)

            logging.info(f"OOS Results: Profit Factor = {oos_pf}, Sharpe Ratio = {oos_sharpe}")

            if oos_pf >= OOS_MIN_PROFIT_FACTOR and oos_sharpe >= OOS_MIN_SHARPE_RATIO:
                logging.info("OOS validation PASSED. Updating live parameters.")
                with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
                    f.write(best_config_str)
                logging.info(f"Successfully updated {BEST_CONFIG_OUTPUT_PATH}")
            else:
                logging.warning("OOS validation FAILED. Parameters will not be updated.")

            # --- Save History ---
            history = {
                "time": time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(job['timestamp'])),
                "trigger_type": job['trigger_type'],
                "is_hours": is_hours,
                "oos_hours": oos_hours,
                "is_profit_factor": study.best_value,
                "is_sharpe_ratio": None, # Not calculated in this version
                "oos_profit_factor": oos_pf,
                "oos_sharpe_ratio": oos_sharpe,
                "validation_passed": oos_pf >= OOS_MIN_PROFIT_FACTOR and oos_sharpe >= OOS_MIN_SHARPE_RATIO,
                "best_params": best_params
            }
            save_optimization_history(history)

            # --- Cleanup ---
            os.remove(JOB_FILE)
            logging.info("Optimization run complete. Waiting for next job.")

        time.sleep(10)

def save_optimization_history(history_data):
    """Saves the optimization run details to the database."""
    import psycopg2
    try:
        conn = psycopg2.connect(
            dbname=os.getenv('DB_NAME'),
            user=os.getenv('DB_USER'),
            password=os.getenv('DB_PASSWORD'),
            host=os.getenv('DB_HOST', 'timescaledb'),
            port=os.getenv('DB_PORT', '5432')
        )
        cursor = conn.cursor()
        query = """
            INSERT INTO optimization_history (
                time, trigger_type, is_hours, oos_hours,
                is_profit_factor, is_sharpe_ratio, oos_profit_factor, oos_sharpe_ratio,
                validation_passed, best_params
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
        """
        cursor.execute(query, (
            history_data['time'],
            history_data['trigger_type'],
            history_data['is_hours'],
            history_data['oos_hours'],
            history_data.get('is_profit_factor'),
            history_data.get('is_sharpe_ratio'),
            history_data.get('oos_profit_factor'),
            history_data.get('oos_sharpe_ratio'),
            history_data['validation_passed'],
            json.dumps(history_data.get('best_params'))
        ))
        conn.commit()
        cursor.close()
        conn.close()
        logging.info("Successfully saved optimization history to database.")
    except Exception as e:
        logging.error(f"Failed to save optimization history: {e}")


if __name__ == "__main__":
    # The Go binary is expected to be built by the main `docker compose up --build` command.
    main()
