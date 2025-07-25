import yaml
import optuna
import numpy as np
from optuna.pruners import BasePruner, HyperbandPruner
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


# --- Application Root ---
APP_ROOT = Path('/app')

# --- Configuration Loading ---
def load_config():
    """Loads configuration from YAML file."""
    config_path = APP_ROOT / 'config' / 'optimizer_config.yaml'
    if not config_path.exists():
        logging.error(f"Configuration file not found at {config_path}")
        logging.error("Please create the config file based on the template or documentation.")
        return None
    with open(config_path, 'r') as f:
        return yaml.safe_load(f)

config = load_config()
if config is None:
    exit(1) # Exit if config loading fails

# --- Environment & Constants ---
# パス設定
PARAMS_DIR = Path(os.getenv('PARAMS_DIR', config['params_dir']))
JOB_FILE = PARAMS_DIR / 'optimization_job.json'
SIMULATION_DIR = APP_ROOT / 'simulation'
CONFIG_TEMPLATE_PATH = PARAMS_DIR / 'trade_config.yaml.template'
BEST_CONFIG_OUTPUT_PATH = PARAMS_DIR / 'trade_config.yaml'
STORAGE_URL = os.getenv('STORAGE_URL', f"sqlite:///{PARAMS_DIR / 'optuna_study.db'}")

# 最適化設定
N_TRIALS = config['n_trials']
MIN_TRADES_FOR_PRUNING = config['min_trades_for_pruning']

# 合格/不合格基準
OOS_MIN_PROFIT_FACTOR = config['oos_min_profit_factor']
OOS_MIN_SHARPE_RATIO = config['oos_min_sharpe_ratio']

# リトライ & 早期停止基準
MAX_RETRY = config['max_retry']
EARLY_STOP_COUNT = config['early_stop_count']
EARLY_STOP_THRESHOLD_RATIO = config['early_stop_threshold_ratio']
TRIGGER_REOPTIMIZE = config['trigger_reoptimize']

# サンプルサイズガード
MIN_EXECUTED_TRADES = config['min_executed_trades']
MIN_ORDER_BOOK_SNAPSHOTS = config['min_order_book_snapshots']

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

def run_simulation(params, sim_csv_path):
    """
    Runs a single Go simulation for a given set of parameters.
    """
    temp_config_path = None
    try:
        # 1. Create a temporary config file from the template and parameters
        with open(CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())
        config_yaml_str = template.render(params)

        with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml', dir=PARAMS_DIR) as tmp:
            tmp.write(config_yaml_str)
            temp_config_path = tmp.name

        # 2. Construct the command to run the Go simulation
        command = [
            str(APP_ROOT / 'build' / 'obi-scalp-bot'),
            '--simulate',
            f'--trade-config={temp_config_path}',
            f'--csv={sim_csv_path}',
            '--json-output'
        ]

        # 3. Execute the command
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            check=True,
            cwd=APP_ROOT
        )

        # 4. Parse the JSON output from stdout
        return json.loads(result.stdout)

    except subprocess.CalledProcessError as e:
        logging.error(f"Simulation failed for config {temp_config_path}: {e.stderr}")
        return None
    except json.JSONDecodeError as e:
        logging.error(f"Failed to parse simulation output for {temp_config_path}: {e}")
        logging.error(f"Received output: {result.stdout}")
        return None
    finally:
        # 5. Clean up the temporary config file
        if temp_config_path and os.path.exists(temp_config_path):
            os.remove(temp_config_path)

def progress_callback(study, trial):
    """
    Callback function to report progress every 100 trials.
    """
    if trial.number % 100 == 0 and trial.number > 0:
        try:
            best_trial = study.best_trial
            logging.info(
                f"Trial {trial.number}: Best trial so far is #{best_trial.number} "
                f"with SQN: {best_trial.value:.2f}, "
                f"PF: {best_trial.user_attrs.get('profit_factor', 0.0):.2f}, "
                f"SR: {best_trial.user_attrs.get('sharpe_ratio', 0.0):.2f}, "
                f"Trades: {best_trial.user_attrs.get('trades', 0)}"
            )
        except ValueError:
            # This can happen if no trial is completed yet
            logging.info(f"Trial {trial.number}: No best trial available yet.")


def objective(trial, study, min_trades_for_pruning: int):
    """Optuna objective function."""
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

    # The CURRENT_SIM_CSV_PATH is now passed to run_simulation directly
    # The objective function itself doesn't need to know which CSV is being used.
    summary = run_simulation(params, study.user_attrs.get('current_csv_path'))


    if summary is None:
        return -1.0 # Return a poor score

    total_trades = summary.get('TotalTrades', 0)
    sharpe_ratio = summary.get('SharpeRatio', 0.0)

    # Store all metrics in user_attrs for later analysis
    trial.set_user_attr("trades", total_trades)
    trial.set_user_attr("sharpe_ratio", sharpe_ratio)
    trial.set_user_attr("profit_factor", summary.get('ProfitFactor', 0.0))


    # Manual Pruning
    if total_trades < min_trades_for_pruning:
        logging.debug(f"Trial {trial.number} pruned with {total_trades} trades (min: {min_trades_for_pruning}).")
        raise optuna.exceptions.TrialPruned()

    # Calculate SQN
    if total_trades > 0 and sharpe_ratio is not None:
        sqn = sharpe_ratio * np.sqrt(total_trades)
    else:
        sqn = -1.0  # Assign a poor score for no trades or invalid SR

    # Report the final value to the pruner
    trial.report(sqn, 1)

    # Check if the trial should be pruned
    if trial.should_prune():
        raise optuna.exceptions.TrialPruned()

    return sqn


def main(run_once=False):
    """Main loop for the optimizer."""
    if not CONFIG_TEMPLATE_PATH.exists():
        logging.error(f"Trade config template not found at {CONFIG_TEMPLATE_PATH}")
        logging.error("Please ensure the template file exists. Exiting.")
        exit(1)

    logging.info("Optimizer started. Waiting for optimization job...")

    while True:
        if not JOB_FILE.exists():
            if run_once:
                logging.info("No job file found and run_once is true. Exiting.")
                break
            time.sleep(10)
            continue

        logging.info(f"Found job file: {JOB_FILE}")
        try:
            with open(JOB_FILE, 'r') as f:
                job = json.load(f)
        except json.JSONDecodeError:
            logging.error("Invalid job file. Deleting.")
            os.remove(JOB_FILE)
            continue

        try:
            # --- Data Export & Validation ---
            is_hours = job['window_is_hours']
            oos_hours = job['window_oos_hours']
            base_n_trials = config.get('n_trials', 100)
            severity = job.get('severity', 'normal')
            n_trials = base_n_trials
            if severity == 'minor':
                n_trials = int(base_n_trials * 2 / 3)
            elif severity == 'major':
                n_trials = int(base_n_trials / 3)

            is_csv_path, oos_csv_path = export_data(is_hours + oos_hours, is_oos_split=True, oos_hours=oos_hours)
            if not is_csv_path or not oos_csv_path:
                logging.error("Failed to get data. Aborting optimization run.")
                os.remove(JOB_FILE)
                continue

            # --- Setup Study ---
            try:
                optuna.delete_study(study_name='obi-scalp-optimization', storage=STORAGE_URL)
            except Exception as e:
                logging.warning(f"Could not delete study, it might not exist: {e}")

            pruner = HyperbandPruner(min_resource=1, max_resource=100, reduction_factor=3)
            study = optuna.create_study(
                study_name='obi-scalp-optimization',
                storage=STORAGE_URL,
                direction='maximize',
                load_if_exists=False,
                pruner=pruner
            )
            catch_exceptions = (sqlalchemy.exc.OperationalError, optuna.exceptions.StorageInternalError, sqlite3.OperationalError)
            min_trades_for_pruning = job.get('min_trades', MIN_TRADES_FOR_PRUNING)
            objective_with_pruning = lambda trial: objective(trial, study, min_trades_for_pruning)

            # --- In-Sample Optimization ---
            logging.info(f"Starting In-Sample optimization with {is_csv_path}")
            study.set_user_attr('current_csv_path', str(is_csv_path))
            study.optimize(
                objective_with_pruning,
                n_trials=n_trials,
                n_jobs=-1,
                show_progress_bar=False,
                catch=catch_exceptions,
                callbacks=[progress_callback],
            )

            try:
                best_trial = study.best_trial
                logging.info(
                    f"Best IS trial found: Trial {best_trial.number} -> "
                    f"SQN: {best_trial.value:.2f}, PF: {best_trial.user_attrs.get('profit_factor', 0.0):.2f}, "
                    f"SR: {best_trial.user_attrs.get('sharpe_ratio', 0.0):.2f}, Trades: {best_trial.user_attrs.get('trades', 0)}"
                )

                # --- Out-of-Sample Validation ---
                oos_validation_passed = False
                best_oos_summary = None
                selected_trial = None
                retries_attempted = 0
                consecutive_failures = 0

                all_trials = study.get_trials(deepcopy=False)
                completed_trials = [t for t in all_trials if t.state == optuna.trial.TrialState.COMPLETE]
                sorted_trials = sorted(completed_trials, key=lambda t: t.value, reverse=True)
                top_n_trials = sorted_trials[:MAX_RETRY]
                logging.info(f"Starting OOS validation for top {len(top_n_trials)} IS trials with {oos_csv_path}")

                for is_rank, trial_to_validate in enumerate(top_n_trials, 1):
                    retries_attempted += 1
                    logging.info(f"--- [Attempt {retries_attempted}/{MAX_RETRY}] OOS Validation for IS Rank #{is_rank} (Trial {trial_to_validate.number}) ---")
                    oos_summary = run_simulation(trial_to_validate.params, oos_csv_path)

                    if oos_summary is None:
                        logging.warning(f"OOS simulation failed for trial {trial_to_validate.number}. Treating as failure.")
                        oos_pf = 0.0
                        oos_sharpe = -999.0
                    else:
                        oos_pf = oos_summary.get('ProfitFactor', 0.0)
                        oos_sharpe = oos_summary.get('SharpeRatio', 0.0)
                        oos_trades = oos_summary.get('TotalTrades', 'N/A')
                        logging.info(f"OOS Result: PF={oos_pf:.2f}, SR={oos_sharpe:.2f}, Trades={oos_trades}")

                    is_pass = oos_pf >= OOS_MIN_PROFIT_FACTOR and oos_sharpe >= OOS_MIN_SHARPE_RATIO
                    if is_pass:
                        logging.info(f"OOS validation PASSED. Selecting this parameter set.")
                        oos_validation_passed = True
                        selected_trial = trial_to_validate
                        best_oos_summary = oos_summary
                        with open(CONFIG_TEMPLATE_PATH, 'r') as f:
                            template = Template(f.read())
                        config_str = template.render(selected_trial.params)
                        with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
                            f.write(config_str)
                        logging.info(f"Successfully updated {BEST_CONFIG_OUTPUT_PATH}")
                        break
                    else:
                        logging.warning(f"OOS validation FAILED for IS Rank #{is_rank}.")

                    is_below_threshold = oos_sharpe < (OOS_MIN_SHARPE_RATIO * EARLY_STOP_THRESHOLD_RATIO)
                    if is_below_threshold:
                        consecutive_failures += 1
                        if consecutive_failures >= EARLY_STOP_COUNT:
                            logging.error("Early stopping condition met. Aborting retry loop.")
                            break
                    else:
                        consecutive_failures = 0

                if not oos_validation_passed:
                    logging.error(f"All top {retries_attempted} IS trials failed OOS validation.")
                    selected_trial = best_trial

                # --- Save History ---
                final_trial = selected_trial
                final_summary = best_oos_summary if oos_validation_passed else {}
                history = {
                    "time": time.strftime('%Y-%m-%d %H:%M:%S', time.gmtime(job['timestamp'])),
                    "trigger_type": job['trigger_type'],
                    "is_hours": is_hours,
                    "oos_hours": oos_hours,
                    "is_sqn": final_trial.value,
                    "is_profit_factor": final_trial.user_attrs.get('profit_factor', 0.0),
                    "is_sharpe_ratio": final_trial.user_attrs.get('sharpe_ratio', 0.0),
                    "is_total_trades": final_trial.user_attrs.get('trades', 0),
                    "oos_profit_factor": final_summary.get('ProfitFactor', 0.0),
                    "oos_sharpe_ratio": final_summary.get('SharpeRatio', 0.0),
                    "oos_total_trades": final_summary.get('TotalTrades', 0),
                    "validation_passed": oos_validation_passed,
                    "best_params": final_trial.params,
                    "is_rank": sorted_trials.index(final_trial) + 1 if final_trial in sorted_trials else -1,
                    "retries_attempted": retries_attempted,
                }
                save_optimization_history(history)

            except ValueError:
                logging.error("No best trial found (all trials may have been pruned). Aborting optimization run.")

        except Exception as e:
            logging.error(f"An unexpected error occurred during the optimization job: {e}", exc_info=True)
        finally:
            # --- Cleanup ---
            if JOB_FILE.exists():
                os.remove(JOB_FILE)
            logging.info("Optimization run complete. Waiting for next job.")

            if run_once:
                logging.info("Job file processed and run_once is true. Exiting.")
                break

        time.sleep(10)

    logging.info("Optimizer shutting down.")

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
                is_sqn, is_profit_factor, is_sharpe_ratio, is_total_trades,
                oos_profit_factor, oos_sharpe_ratio, oos_total_trades,
                validation_passed, best_params, is_rank, retries_attempted
            ) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
        """
        cursor.execute(query, (
            history_data['time'],
            history_data['trigger_type'],
            history_data['is_hours'],
            history_data['oos_hours'],
            history_data.get('is_sqn'),
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
    # Check for a special argument to run only once, for testing.
    import sys
    run_once = '--run-once' in sys.argv
    main(run_once=run_once)
