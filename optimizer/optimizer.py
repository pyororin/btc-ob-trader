import yaml
import optuna
import numpy as np
from optuna.pruners import HyperbandPruner
import sqlalchemy
import sqlite3
import subprocess
import os
import json
import tempfile
import logging
import time
from datetime import datetime, timedelta
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
CONFIG_TEMPLATE_PATH = PARAMS_DIR / 'trade_config.yaml.template'
BEST_CONFIG_OUTPUT_PATH = PARAMS_DIR / 'trade_config.yaml'
N_TRIALS = int(os.getenv('N_TRIALS', '100'))
STORAGE_URL = os.getenv('STORAGE_URL', f"sqlite:///{PARAMS_DIR / 'optuna_study.db'}")

# --- Pass/Fail Criteria ---
OOS_MIN_PROFIT_FACTOR = 1.2
OOS_MIN_SHARPE_RATIO = 0.5
# OOS_MAX_DRAWDOWN_PERCENTILE = 0.85 # This is complex and needs historical data. Skipped for now.

# --- Retry & Early Stopping Criteria ---
MAX_RETRY = 5
EARLY_STOP_COUNT = 2
EARLY_STOP_THRESHOLD_RATIO = 0.7

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
        '--no-zip',
        f'--trade-config={BEST_CONFIG_OUTPUT_PATH}'
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

            if not data_lines:
                logging.warning("No data to split.")
                return is_path, oos_path

            # Assuming the first column is the timestamp
            first_timestamp_str = data_lines[0].split(',')[0]
            last_timestamp_str = data_lines[-1].split(',')[0]

            def parse_timestamp(ts_str):
                # The incoming format is like '2025-07-24 03:06:51.769817+00'
                # The +00 timezone can be handled by strptime if we remove the colon.
                # However, Python's %z directive expects HHMM, not HH.
                # A more robust way is to handle the timezone separately.
                if ts_str.endswith('+00'):
                    ts_str = ts_str[:-3] + '+0000'
                elif ts_str.endswith('Z'):
                     ts_str = ts_str[:-1] + '+0000'

                # Try parsing with and without fractional seconds
                try:
                    return datetime.strptime(ts_str, '%Y-%m-%d %H:%M:%S.%f%z')
                except ValueError:
                    return datetime.strptime(ts_str, '%Y-%m-%d %H:%M:%S%z')

            try:
                first_time = parse_timestamp(first_timestamp_str)
                last_time = parse_timestamp(last_timestamp_str)
            except ValueError as e:
                logging.error(f"Could not parse timestamps: '{first_timestamp_str}' and '{last_timestamp_str}' with error: {e}")
                # Fallback to line-based split if time parsing fails
                split_index = int(len(data_lines) * is_ratio)
                f_is.write(header)
                f_is.writelines(data_lines[:split_index])
                f_oos.write(header)
                f_oos.writelines(data_lines[split_index:])
                return is_path, oos_path


            total_duration_seconds = (last_time - first_time).total_seconds()
            is_duration_seconds = total_duration_seconds * is_ratio
            split_time = first_time + timedelta(seconds=is_duration_seconds)

            f_is.write(header)
            f_oos.write(header)

            split_found = False
            for line in data_lines:
                current_time_str = line.split(',')[0]
                current_time = parse_timestamp(current_time_str)

                if current_time < split_time:
                    f_is.write(line)
                else:
                    f_oos.write(line)
                    split_found = True

            if not split_found:
                logging.warning("Split time was after all data points. OOS will be empty.")

        with open(is_path, 'r') as f_is:
            is_lines = len(f_is.readlines())
        with open(oos_path, 'r') as f_oos:
            oos_lines = len(f_oos.readlines())

        logging.info(f"Split data into IS ({is_path}, {is_lines} lines) and OOS ({oos_path}, {oos_lines} lines)")
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
        'adaptive_position_sizing_enabled': trial.suggest_categorical('adaptive_position_sizing_enabled', [True, False]),
        'adaptive_num_trades': trial.suggest_int('adaptive_num_trades', 3, 20),
        'adaptive_reduction_step': trial.suggest_float('adaptive_reduction_step', 0.5, 1.0),
        'adaptive_min_ratio': trial.suggest_float('adaptive_min_ratio', 0.1, 0.8),
        'long_obi_threshold': trial.suggest_float('long_obi_threshold', 0.1, 2.0),
        'long_tp': trial.suggest_int('long_tp', 10, 500),
        'long_sl': trial.suggest_int('long_sl', -500, -10),
        'short_obi_threshold': trial.suggest_float('short_obi_threshold', -2.0, -0.1),
        'short_tp': trial.suggest_int('short_tp', 10, 500),
        'short_sl': trial.suggest_int('short_sl', -500, -10),
        'hold_duration_ms': trial.suggest_int('hold_duration_ms', 100, 2000),
        'slope_filter_enabled': trial.suggest_categorical('slope_filter_enabled', [True, False]),
        'slope_period': trial.suggest_int('slope_period', 3, 50),
        'slope_threshold': trial.suggest_float('slope_threshold', 0.0, 0.5),
        'ewma_lambda': trial.suggest_float('ewma_lambda', 0.05, 0.3),
        'dynamic_obi_enabled': trial.suggest_categorical('dynamic_obi_enabled', [True, False]),
        'volatility_factor': trial.suggest_float('volatility_factor', 0.5, 5.0),
        'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.5, 1.0),
        'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.0, 3.0),
        'twap_enabled': trial.suggest_categorical('twap_enabled', [True, False]),
        'twap_max_order_size_btc': trial.suggest_float('twap_max_order_size_btc', 0.01, 0.1),
        'twap_interval_seconds': trial.suggest_int('twap_interval_seconds', 1, 10),
        'twap_partial_exit_enabled': trial.suggest_categorical('twap_partial_exit_enabled', [True, False]),
        'twap_profit_threshold': trial.suggest_float('twap_profit_threshold', 0.1, 2.0),
        'twap_exit_ratio': trial.suggest_float('twap_exit_ratio', 0.1, 1.0),
        'risk_max_drawdown_percent': trial.suggest_int('risk_max_drawdown_percent', 15, 25),
        'risk_max_position_ratio': trial.suggest_float('risk_max_position_ratio', 0.5, 0.9),
    }

    with open(CONFIG_TEMPLATE_PATH, 'r') as f:
        template = Template(f.read())

    rendered_config_str = template.render(params)

    with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml') as temp_config_file:
        temp_config_file.write(rendered_config_str)
        temp_config_path = temp_config_file.name

    summary = run_simulation(temp_config_path, CURRENT_SIM_CSV_PATH)
    os.remove(temp_config_path)

    if summary is None or summary.get('TotalTrades', 0) == 0:
        # Penalize trials that result in no trades
        return -1.0, -1.0, 0

    # The metrics to optimize
    profit_factor = summary.get('ProfitFactor', 0.0)
    sharpe_ratio = summary.get('SharpeRatio', 0.0)
    total_trades = summary.get('TotalTrades', 0)

    # Handle cases where PF is infinite (no losses)
    if profit_factor > 1e6:
        profit_factor = 1e6

    return profit_factor, sharpe_ratio, total_trades


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
                directions=['maximize', 'maximize', 'maximize'],
                pruner=HyperbandPruner()
            )
            catch_exceptions = (
                sqlalchemy.exc.OperationalError,
                optuna.exceptions.StorageInternalError,
                sqlite3.OperationalError,
            )
            study.optimize(
                objective,
                n_trials=N_TRIALS,
                n_jobs=-1,
                show_progress_bar=True,
                catch=catch_exceptions,
            )

            best_trials = study.best_trials
            if not best_trials:
                logging.error("No best trials found. Aborting optimization run.")
                os.remove(JOB_FILE)
                continue

            # --- IS Trials Ranking ---
            logging.info(f"Ranking the {len(best_trials)} best trials from In-Sample optimization...")
            pfs = np.array([t.values[0] for t in best_trials])
            srs = np.array([t.values[1] for t in best_trials])

            # Z-score normalization
            def z_score_normalize(data):
                mean = np.mean(data)
                std = np.std(data)
                if std == 0:
                    return np.zeros_like(data)
                return (data - mean) / std

            pfs_z = z_score_normalize(pfs)
            srs_z = z_score_normalize(srs)
            combined_scores = pfs_z + srs_z

            # Sort trials by combined score in descending order
            sorted_indices = np.argsort(combined_scores)[::-1]
            sorted_trials = [best_trials[i] for i in sorted_indices]

            logging.info("Top 5 IS trials (Trial #, PF, SR, Trades, Z-Score):")
            for i, trial in enumerate(sorted_trials[:5]):
                trades = trial.values[2] if len(trial.values) > 2 else 'N/A'
                logging.info(f"  Rank {i+1}: Trial {trial.number}, PF: {trial.values[0]:.2f}, SR: {trial.values[1]:.2f}, Trades: {trades}, Score: {combined_scores[sorted_indices[i]]:.2f}")

            # --- Out-of-Sample Validation with Retry ---
            oos_validation_passed = False
            best_oos_trial = None
            best_oos_summary = None
            retries_attempted = 0
            consecutive_severe_failures = 0

            for i, trial_to_validate in enumerate(sorted_trials[:MAX_RETRY]):
                is_rank = i + 1
                retries_attempted = is_rank
                logging.info(f"--- OOS Validation Attempt #{is_rank}/{MAX_RETRY} (IS Trial: {trial_to_validate.number}) ---")

                current_params = trial_to_validate.params
                with open(CONFIG_TEMPLATE_PATH, 'r') as f:
                    template = Template(f.read())
                config_str = template.render(current_params)

                with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml') as temp_config_file:
                    temp_config_file.write(config_str)
                    temp_config_path = temp_config_file.name

                oos_summary = run_simulation(temp_config_path, oos_csv_path)
                os.remove(temp_config_path)

                if oos_summary is None:
                    logging.warning(f"OOS simulation failed for IS-Rank {is_rank}. Skipping.")
                    consecutive_severe_failures = 0 # Reset on simulation error
                    continue

                oos_pf = oos_summary.get('ProfitFactor', 0.0)
                oos_sharpe = oos_summary.get('SharpeRatio', 0.0)
                oos_trades = oos_summary.get('TotalTrades', 'N/A')
                logging.info(f"OOS Result for IS-Rank {is_rank}: PF={oos_pf:.2f}, SR={oos_sharpe:.2f}, Trades={oos_trades}")

                if oos_pf >= OOS_MIN_PROFIT_FACTOR and oos_sharpe >= OOS_MIN_SHARPE_RATIO:
                    logging.info(f"OOS validation PASSED for IS-Rank {is_rank}. Selecting this parameter set.")
                    oos_validation_passed = True
                    best_oos_trial = trial_to_validate
                    best_oos_summary = oos_summary

                    # Update config file
                    with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
                        f.write(config_str)
                    logging.info(f"Successfully updated {BEST_CONFIG_OUTPUT_PATH}")
                    break # Exit retry loop on first pass
                else:
                    logging.info(f"OOS validation FAILED for IS-Rank {is_rank}.")
                    # Check for early stopping condition
                    if oos_sharpe < OOS_MIN_SHARPE_RATIO * EARLY_STOP_THRESHOLD_RATIO:
                        consecutive_severe_failures += 1
                        logging.warning(f"Severe failure detected. Consecutive count: {consecutive_severe_failures}/{EARLY_STOP_COUNT}")
                    else:
                        consecutive_severe_failures = 0 # Reset if failure is not severe

                    if consecutive_severe_failures >= EARLY_STOP_COUNT:
                        logging.error(f"Early stopping triggered after {consecutive_severe_failures} consecutive severe failures.")
                        break

            if not oos_validation_passed:
                logging.warning(f"OOS validation failed for all top {retries_attempted} IS trials.")


            # --- Save History ---
            # Determine which trial's data to save
            final_trial = best_oos_trial if oos_validation_passed else sorted_trials[0]
            final_summary = best_oos_summary if oos_validation_passed else {} # Use empty dict if all failed

            history = {
                "time": time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(job['timestamp'])),
                "trigger_type": job['trigger_type'],
                "is_hours": is_hours,
                "oos_hours": oos_hours,
                "is_profit_factor": final_trial.values[0],
                "is_sharpe_ratio": final_trial.values[1],
                "is_total_trades": final_trial.values[2] if len(final_trial.values) > 2 else 0,
                "oos_profit_factor": final_summary.get('ProfitFactor', 0.0),
                "oos_sharpe_ratio": final_summary.get('SharpeRatio', 0.0),
                "oos_total_trades": final_summary.get('TotalTrades', 0),
                "validation_passed": oos_validation_passed,
                "best_params": final_trial.params,
                "is_rank": retries_attempted if oos_validation_passed else None,
                "retries_attempted": retries_attempted
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
                is_profit_factor, is_sharpe_ratio, is_total_trades,
                oos_profit_factor, oos_sharpe_ratio, oos_total_trades,
                validation_passed, best_params, is_rank, retries_attempted
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
        """
        cursor.execute(query, (
            history_data['time'],
            history_data['trigger_type'],
            history_data['is_hours'],
            history_data['oos_hours'],
            history_data.get('is_profit_factor'),
            history_data.get('is_sharpe_ratio'),
            history_data.get('is_total_trades'),
            history_data.get('oos_profit_factor'),
            history_data.get('oos_sharpe_ratio'),
            history_data.get('oos_total_trades'),
            history_data['validation_passed'],
            json.dumps(history_data.get('best_params')),
            history_data.get('is_rank'),
            history_data.get('retries_attempted')
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
