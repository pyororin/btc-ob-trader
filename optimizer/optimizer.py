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
from sklearn.preprocessing import MinMaxScaler


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
OOS_MIN_TRADES = config.get('oos_min_trades', 10)

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
    cmd = [
        'go',
        'run',
        'cmd/export/main.go',
        f'--hours-before={int(hours_before)}',
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
    temp_config_file = None
    try:
        # 1. Create a temporary config file that persists until explicitly deleted
        with open(CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())
        config_yaml_str = template.render(params)

        # Use delete=False, so the file is not deleted when the 'with' block exits.
        # This is crucial for the subprocess to be able to access it.
        temp_config_file = tempfile.NamedTemporaryFile(
            mode='w', delete=False, suffix='.yaml', dir=str(PARAMS_DIR)
        )
        temp_config_file.write(config_yaml_str)
        temp_config_file.close() # Close the file to ensure it's written to disk

        temp_config_path = temp_config_file.name

        # 2. Construct the command to run the Go simulation
        command = [
            'go', 'run', 'cmd/bot/main.go',
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
        # Provide more context in the error log
        logging.error(f"Simulation failed for config {getattr(temp_config_file, 'name', 'N/A')}: {e.stderr}")
        return {}
    except json.JSONDecodeError as e:
        logging.error(f"Failed to parse simulation output for {getattr(temp_config_file, 'name', 'N/A')}: {e}")
        if 'result' in locals():
            logging.error(f"Received output: {result.stdout}")
        return {}
    finally:
        # 5. Manually clean up the temporary config file
        if temp_config_file and os.path.exists(temp_config_file.name):
            try:
                os.remove(temp_config_file.name)
            except OSError as e:
                logging.error(f"Failed to remove temporary config file {temp_config_file.name}: {e}")

def progress_callback(study, trial):
    """
    Callback function to report progress every 100 trials.
    """
    if trial.number % 100 == 0 and trial.number > 0:
        try:
            # During optimization, best_trial is based on the simple objective (Sharpe Ratio)
            # This provides a consistent progress update.
            # The final "best" trial will be determined by the custom multi-metric objective.
            best_trial_so_far = study.best_trial
            logging.info(
                f"Trial {trial.number}: Best trial so far is #{best_trial_so_far.number} "
                f"with SR: {best_trial_so_far.value:.2f}, "
                f"PF: {best_trial_so_far.user_attrs.get('profit_factor', 0.0):.2f}, "
                f"Trades: {best_trial_so_far.user_attrs.get('trades', 0)}"
            )
        except ValueError:
            # This can happen if no trial is completed yet
            logging.info(f"Trial {trial.number}: No best trial available yet.")


def calculate_max_drawdown(pnl_history):
    """Calculates the maximum drawdown from a PnL history."""
    if not pnl_history:
        return 0.0, 0.0

    pnl_array = np.array(pnl_history)
    cumulative_pnl = np.cumsum(pnl_array)
    peak = np.maximum.accumulate(cumulative_pnl)
    drawdown = peak - cumulative_pnl
    max_drawdown = np.max(drawdown)

    # Calculate relative drawdown
    final_pnl = cumulative_pnl[-1] if len(cumulative_pnl) > 0 else 0
    relative_drawdown = max_drawdown / (final_pnl + 1e-6) # Add epsilon to avoid division by zero

    return float(max_drawdown), float(relative_drawdown)


class ObjectiveMetrics:
    """A simple class to hold and scale metrics for the objective function."""
    def __init__(self, weights):
        self.weights = weights
        self.metrics = {
            'sharpe_ratio': [],
            'profit_factor': [],
            'relative_drawdown': [],
            'sqn': [],
            'trades': []
        }
        self.scalers = {
            'sharpe_ratio': MinMaxScaler(),
            'profit_factor': MinMaxScaler(),
            'relative_drawdown': MinMaxScaler(), # We will scale drawdown so smaller is better
            'sqn': MinMaxScaler(),
            'trades': MinMaxScaler()
        }

    def add(self, trial_metrics):
        for key in self.metrics:
            if key in trial_metrics:
                self.metrics[key].append(trial_metrics[key])

    def fit_scalers(self):
        for key, values in self.metrics.items():
            if values:
                # Scaler expects a 2D array
                self.scalers[key].fit(np.array(values).reshape(-1, 1))

    def calculate_objective(self, trial_metrics):
        scaled_values = {}
        for key, scaler in self.scalers.items():
            value = trial_metrics.get(key, 0.0)
            if hasattr(scaler, 'data_max_') and hasattr(scaler, 'data_min_') and scaler.data_max_ is not None and scaler.data_min_ is not None and scaler.data_max_ != scaler.data_min_:
                 # Scaler expects a 2D array
                scaled_value = scaler.transform(np.array([[value]]))[0][0]
            else:
                scaled_value = 0.5 # Default value if scaling is not possible

            # For drawdown, a smaller value is better. We invert the score.
            if key == 'relative_drawdown':
                scaled_value = 1.0 - scaled_value

            scaled_values[key] = scaled_value

        # Calculate weighted average
        objective_value = 0.0
        total_weight = 0.0
        for key, weight in self.weights.items():
            objective_value += scaled_values.get(key, 0.0) * weight
            total_weight += weight

        return objective_value / total_weight if total_weight > 0 else 0.0


def objective(trial, study, min_trades_for_pruning: int):
    """Optuna objective function."""
    params = {
        'spread_limit': trial.suggest_int('spread_limit', 20, 80),
        'lot_max_ratio': trial.suggest_float('lot_max_ratio', 0.02, 0.1),
        'order_ratio': trial.suggest_float('order_ratio', 0.08, 0.2),
        'adaptive_position_sizing_enabled': trial.suggest_categorical('adaptive_position_sizing_enabled', [True, False]),
        'adaptive_num_trades': trial.suggest_int('adaptive_num_trades', 5, 15),
        'adaptive_reduction_step': trial.suggest_float('adaptive_reduction_step', 0.6, 0.9),
        'adaptive_min_ratio': trial.suggest_float('adaptive_min_ratio', 0.2, 0.7),
        'long_obi_threshold': trial.suggest_float('long_obi_threshold', 0.2, 1.5),
        'long_tp': trial.suggest_int('long_tp', 50, 250),
        'long_sl': trial.suggest_int('long_sl', -250, -50),
        'short_obi_threshold': trial.suggest_float('short_obi_threshold', -1.5, -0.2),
        'short_tp': trial.suggest_int('short_tp', 50, 250),
        'short_sl': trial.suggest_int('short_sl', -250, -50),
        'hold_duration_ms': trial.suggest_int('hold_duration_ms', 200, 1000),
        'slope_filter_enabled': trial.suggest_categorical('slope_filter_enabled', [True, False]),
        'slope_period': trial.suggest_int('slope_period', 5, 30),
        'slope_threshold': trial.suggest_float('slope_threshold', 0.05, 0.3),
        'ewma_lambda': trial.suggest_float('ewma_lambda', 0.1, 0.25),
        'dynamic_obi_enabled': trial.suggest_categorical('dynamic_obi_enabled', [True, False]),
        'volatility_factor': trial.suggest_float('volatility_factor', 1.0, 3.0),
        'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.6, 0.9),
        'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.1, 2.0),
        'twap_enabled': trial.suggest_categorical('twap_enabled', [True, False]),
        'twap_max_order_size_btc': trial.suggest_float('twap_max_order_size_btc', 0.02, 0.08),
        'twap_interval_seconds': trial.suggest_int('twap_interval_seconds', 2, 8),
        'twap_partial_exit_enabled': trial.suggest_categorical('twap_partial_exit_enabled', [True, False]),
        'twap_profit_threshold': trial.suggest_float('twap_profit_threshold', 0.2, 1.5),
        'twap_exit_ratio': trial.suggest_float('twap_exit_ratio', 0.2, 0.8),
        'risk_max_drawdown_percent': trial.suggest_int('risk_max_drawdown_percent', 18, 22),
        'risk_max_position_ratio': trial.suggest_float('risk_max_position_ratio', 0.6, 0.8),
        'composite_threshold': trial.suggest_float('composite_threshold', 0.2, 1.5),
        'obi_weight': trial.suggest_float('obi_weight', 0.5, 1.5),
        'ofi_weight': trial.suggest_float('ofi_weight', 0.2, 1.0),
        'cvd_weight': trial.suggest_float('cvd_weight', 0.2, 1.0),
        'micro_price_weight': trial.suggest_float('micro_price_weight', 0.2, 1.0),
    }

    summary = run_simulation(params, study.user_attrs.get('current_csv_path'))

    if not isinstance(summary, dict) or not summary:
        # Prune if simulation fails
        raise optuna.exceptions.TrialPruned()

    total_trades = summary.get('TotalTrades', 0)
    sharpe_ratio = summary.get('SharpeRatio', 0.0)
    profit_factor = summary.get('ProfitFactor', 0.0)
    pnl_history = summary.get('PnlHistory', [])
    max_drawdown, relative_drawdown = calculate_max_drawdown(pnl_history)

    # Calculate SQN for logging, but it's not the main objective anymore
    sqn = sharpe_ratio * np.sqrt(total_trades) if total_trades > 0 and sharpe_ratio is not None else 0.0

    # Store all metrics in user_attrs for later analysis
    trial.set_user_attr("trades", total_trades)
    trial.set_user_attr("sharpe_ratio", sharpe_ratio)
    trial.set_user_attr("profit_factor", profit_factor)
    trial.set_user_attr("max_drawdown", max_drawdown)
    trial.set_user_attr("relative_drawdown", relative_drawdown)
    trial.set_user_attr("sqn", sqn)

    # Pruning based on trade count
    if total_trades < min_trades_for_pruning:
        logging.debug(f"Trial {trial.number} pruned with {total_trades} trades (min: {min_trades_for_pruning}).")
        raise optuna.exceptions.TrialPruned()

    # The objective for Optuna's pruner is kept simple (e.g., Sharpe Ratio).
    # It's generally more robust than SQN, which can be skewed by a high number of trades.
    objective_for_pruner = sharpe_ratio
    trial.report(objective_for_pruner, 1)

    if trial.should_prune():
        raise optuna.exceptions.TrialPruned()

    # The final "best" trial will be re-evaluated based on the multi-metric custom objective.
    return objective_for_pruner


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
            study_name = f"obi-scalp-optimization-{int(time.time())}"
            logging.info(f"Creating new Optuna study: {study_name}")
            pruner = HyperbandPruner(min_resource=5, max_resource=100, reduction_factor=3)
            sampler = optuna.samplers.CmaEsSampler(warn_independent_sampling=False)
            study = optuna.create_study(
                study_name=study_name,
                storage=STORAGE_URL,
                direction='maximize',
                load_if_exists=False,
                pruner=pruner,
                sampler=sampler
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

            # --- Post-optimization Analysis with Custom Objective ---
            logging.info("Optimization finished. Calculating final objective scores.")

            completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
            if not completed_trials:
                logging.warning("No trials were completed successfully. Skipping post-optimization analysis.")
                continue

            objective_weights = config.get('objective_weights', {
                'sharpe_ratio': 1.5,
                'profit_factor': 1.0,
                'relative_drawdown': 1.0,
                'sqn': 0.5,
                'trades': 0.5
            })
            metrics_calculator = ObjectiveMetrics(weights=objective_weights)

            for trial in completed_trials:
                metrics_calculator.add(trial.user_attrs)

            metrics_calculator.fit_scalers()

            scored_trials = []
            for trial in completed_trials:
                final_score = metrics_calculator.calculate_objective(trial.user_attrs)
                scored_trials.append((trial, final_score))

            scored_trials.sort(key=lambda x: x[1], reverse=True)

            if not scored_trials:
                 raise ValueError("No scored trials available.")

            best_trial, best_score = scored_trials[0]

            study.set_user_attr("best_trial_by_custom_objective", {
                "number": best_trial.number,
                "value": best_score,
                "params": best_trial.params,
                "user_attrs": best_trial.user_attrs
            })

            logging.info(
                f"Best trial by custom objective: Trial #{best_trial.number} -> "
                f"Score: {best_score:.4f}, "
                f"SR: {best_trial.user_attrs.get('sharpe_ratio', 0.0):.2f}, "
                f"PF: {best_trial.user_attrs.get('profit_factor', 0.0):.2f}, "
                f"Trades: {best_trial.user_attrs.get('trades', 0)}"
            )

            # --- Parameter Analysis and OOS Validation ---
            logging.info("Starting parameter analysis and OOS validation...")
            oos_validation_passed = False
            best_oos_summary = None
            selected_params = None
            final_is_trial = None # The IS trial that passed OOS validation
            retries_attempted = 0

            oos_candidates = []
            try:
                analyzer_command = ['python3', str(APP_ROOT / 'optimizer' / 'analyzer.py'), '--study-name', study.study_name]
                result = subprocess.run(analyzer_command, capture_output=True, text=True, check=True, cwd=APP_ROOT)
                robust_params = json.loads(result.stdout)
                logging.info("Analyzer recommended robust parameters as first candidate.")
                oos_candidates.append({'params': robust_params, 'trial': best_trial, 'source': 'analyzer'})
            except (subprocess.CalledProcessError, json.JSONDecodeError) as e:
                logging.warning(f"Parameter analysis failed: {e}. Proceeding with top IS trials only.")

            for rank, (trial, score) in enumerate(scored_trials):
                 oos_candidates.append({'params': trial.params, 'trial': trial, 'source': f'is_rank_{rank+1}'})

            early_stop_trigger_count = 0
            early_stop_threshold = OOS_MIN_SHARPE_RATIO * EARLY_STOP_THRESHOLD_RATIO

            for i, candidate in enumerate(oos_candidates):
                if i >= MAX_RETRY:
                    logging.warning(f"Reached max_retry limit of {MAX_RETRY}. Stopping OOS validation.")
                    break

                retries_attempted += 1
                current_params = candidate['params']
                current_trial = candidate['trial']
                source = candidate['source']

                logging.info(f"--- Running OOS Validation attempt #{retries_attempted} (source: {source}) ---")
                oos_summary = run_simulation(current_params, oos_csv_path)

                if not isinstance(oos_summary, dict) or not oos_summary:
                    logging.warning("OOS simulation failed or returned empty results.")
                    continue

                oos_pf = oos_summary.get('ProfitFactor', 0.0)
                oos_sharpe = oos_summary.get('SharpeRatio', 0.0)
                oos_trades = oos_summary.get('TotalTrades', 0)
                logging.info(f"OOS Result: PF={oos_pf:.2f}, SR={oos_sharpe:.2f}, Trades={oos_trades}")

                passed_trades = oos_trades >= OOS_MIN_TRADES
                passed_pf = oos_pf >= OOS_MIN_PROFIT_FACTOR
                passed_sr = oos_sharpe >= OOS_MIN_SHARPE_RATIO

                if passed_trades and passed_pf and passed_sr:
                    logging.info(f"OOS validation PASSED for attempt #{retries_attempted}.")
                    oos_validation_passed = True
                    best_oos_summary = oos_summary
                    selected_params = current_params
                    final_is_trial = current_trial
                    with open(CONFIG_TEMPLATE_PATH, 'r') as f:
                        template = Template(f.read())
                    config_str = template.render(selected_params)
                    with open(BEST_CONFIG_OUTPUT_PATH, 'w') as f:
                        f.write(config_str)
                    logging.info(f"Successfully updated {BEST_CONFIG_OUTPUT_PATH}")
                    break
                else:
                    reasons = []
                    if not passed_trades: reasons.append(f"trades < {OOS_MIN_TRADES}")
                    if not passed_pf: reasons.append(f"PF < {OOS_MIN_PROFIT_FACTOR}")
                    if not passed_sr: reasons.append(f"SR < {OOS_MIN_SHARPE_RATIO}")
                    fail_reason_str = ", ".join(reasons)
                    fail_log_message = f"OOS validation FAILED for attempt #{retries_attempted} ({fail_reason_str})."
                    if oos_sharpe < early_stop_threshold:
                        early_stop_trigger_count += 1
                        fail_log_message += f" Sharpe ratio below early stop threshold. Trigger count: {early_stop_trigger_count}/{EARLY_STOP_COUNT}"
                    else:
                        early_stop_trigger_count = 0
                    logging.warning(fail_log_message)
                    if early_stop_trigger_count >= EARLY_STOP_COUNT:
                        logging.error("Early stopping triggered due to consecutively poor OOS performance. Aborting retries.")
                        break

            if not oos_validation_passed:
                logging.warning("OOS validation failed for all attempted parameter sets.")

        except (Exception, ValueError) as e:
            logging.error(f"An unexpected error occurred during the optimization job: {e}", exc_info=True)
        finally:
            if JOB_FILE.exists():
                os.remove(JOB_FILE)
            logging.info("Optimization run complete. Waiting for next job.")
            if run_once:
                break
        time.sleep(10)

    logging.info("Optimizer shutting down.")


if __name__ == "__main__":
    import sys
    run_once = '--run-once' in sys.argv
    main(run_once=run_once)
