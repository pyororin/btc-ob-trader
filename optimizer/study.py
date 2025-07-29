import optuna
import logging
import subprocess
import json
from pathlib import Path
from jinja2 import Template
from typing import Union

from . import config
from .objective import Objective, MetricsCalculator
from .simulation import run_simulation

def create_study() -> optuna.Study:
    """
    Creates and configures a new Optuna study.

    It uses a HyperbandPruner for efficient early stopping of unpromising trials
    and a CMA-ES sampler for effective hyperparameter searching.

    Returns:
        An optuna.Study object.
    """
    study_name = f"obi-scalp-optimization-{int(optuna.datetime.datetime.now().timestamp())}"
    logging.info(f"Creating new Optuna study: {study_name}")

    pruner = optuna.pruners.HyperbandPruner(min_resource=5, max_resource=100, reduction_factor=3)
    sampler = optuna.samplers.CmaEsSampler(warn_independent_sampling=False)

    return optuna.create_study(
        study_name=study_name,
        storage=config.STORAGE_URL,
        direction='maximize',
        load_if_exists=False,
        pruner=pruner,
        sampler=sampler
    )

def run_optimization(study: optuna.Study, is_csv_path: Path, n_trials: int):
    """
    Runs the main optimization loop for a given study.

    Args:
        study: The Optuna study to optimize.
        is_csv_path: Path to the In-Sample data CSV file.
        n_trials: The number of trials to run.
    """
    study.set_user_attr('current_csv_path', str(is_csv_path))
    objective_func = Objective(study)

    logging.info(f"Starting In-Sample optimization with {is_csv_path} for {n_trials} trials.")

    study.optimize(
        objective_func,
        n_trials=n_trials,
        n_jobs=-1,  # Use all available CPU cores
        show_progress_bar=False, # Logging callback is used instead
        catch=(Exception,), # Catch all exceptions to prevent optimizer crash
        callbacks=[progress_callback]
    )

def progress_callback(study: optuna.Study, trial: optuna.Trial):
    """Callback function to report progress periodically."""
    if trial.number > 0 and trial.number % 100 == 0:
        try:
            best_trial = study.best_trial
            logging.info(
                f"Trial {trial.number}/{study.n_trials}: Best trial so far is #{best_trial.number} "
                f"with SR: {best_trial.value:.2f}, "
                f"PF: {best_trial.user_attrs.get('profit_factor', 0.0):.2f}, "
                f"Trades: {best_trial.user_attrs.get('trades', 0)}"
            )
        except ValueError:
            logging.info(f"Trial {trial.number}/{study.n_trials}: No completed trials yet.")

def analyze_and_validate(study: optuna.Study, oos_csv_path: Path) -> bool:
    """
    Analyzes the completed study, selects candidate parameters, and performs OOS validation.

    This function performs the following steps:
    1. Re-scores all completed trials using a multi-metric `MetricsCalculator`.
    2. Identifies a list of candidate parameters (best IS trial, robust params from analyzer).
    3. Iteratively tests candidates against the OOS data.
    4. If a candidate passes OOS validation, its parameters are saved as the new best config.

    Args:
        study: The completed Optuna study.
        oos_csv_path: Path to the Out-of-Sample data CSV file.

    Returns:
        True if OOS validation passed and a new config was saved, False otherwise.
    """
    logging.info("Optimization finished. Analyzing results and performing OOS validation.")

    completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("No trials were completed successfully. Skipping OOS validation.")
        return False

    # 1. Re-score trials with the custom multi-metric objective
    metrics_calculator = MetricsCalculator(config.OBJECTIVE_WEIGHTS, completed_trials)
    scored_trials = sorted(
        [(trial, metrics_calculator.calculate_score(trial)) for trial in completed_trials],
        key=lambda x: x[1],
        reverse=True
    )
    best_is_trial, best_is_score = scored_trials[0]
    logging.info(
        f"Best trial by custom objective: #{best_is_trial.number} (Score: {best_is_score:.4f}, "
        f"SR: {best_is_trial.user_attrs.get('sharpe_ratio', 0.0):.2f}, "
        f"Trades: {best_is_trial.user_attrs.get('trades', 0)})"
    )

    # 2. Build a list of candidate parameters for OOS validation
    candidates = _get_oos_candidates(study, scored_trials)

    # 3. Perform OOS validation on candidates
    return _perform_oos_validation(candidates, oos_csv_path)

def _get_oos_candidates(study: optuna.Study, scored_trials: list) -> list[dict]:
    """
    Generates a list of parameter candidates for OOS validation.

    The list includes parameters from the analyzer and the top-ranked IS trials.
    """
    candidates = []
    # First candidate: robust parameters from the analyzer
    try:
        analyzer_command = ['python3', str(config.APP_ROOT / 'optimizer' / 'analyzer.py'), '--study-name', study.study_name]
        result = subprocess.run(analyzer_command, capture_output=True, text=True, check=True, cwd=config.APP_ROOT)
        robust_params = json.loads(result.stdout)
        if robust_params:
            candidates.append({'params': robust_params, 'source': 'analyzer'})
            logging.info("Analyzer recommended robust parameters as first candidate.")
        else:
            logging.warning("Analyzer returned no parameters. Proceeding with top IS trials only.")
    except subprocess.CalledProcessError as e:
        logging.warning(f"Parameter analyzer failed with exit code {e.returncode}. Stderr: {e.stderr.strip()}. Proceeding with top IS trials only.")
    except json.JSONDecodeError as e:
        logging.warning(f"Failed to decode JSON from analyzer: {e}. Proceeding with top IS trials only.")

    # Subsequent candidates: top N trials from IS optimization
    for rank, (trial, score) in enumerate(scored_trials):
        candidates.append({'params': trial.params, 'source': f'is_rank_{rank+1}'})

    return candidates

def _perform_oos_validation(candidates: list, oos_csv_path: Path) -> bool:
    """Iteratively validates candidate parameters against OOS data."""
    early_stop_trigger_count = 0
    early_stop_threshold = config.OOS_MIN_SHARPE_RATIO * config.EARLY_STOP_THRESHOLD_RATIO

    for i, candidate in enumerate(candidates):
        if i >= config.MAX_RETRY:
            logging.warning(f"Reached max_retry limit of {config.MAX_RETRY}. Stopping OOS validation.")
            break

        logging.info(f"--- Running OOS Validation attempt #{i+1} (source: {candidate['source']}) ---")
        oos_summary = run_simulation(candidate['params'], oos_csv_path)

        if not isinstance(oos_summary, dict) or not oos_summary:
            logging.warning("OOS simulation failed or returned empty results.")
            continue

        oos_pf = oos_summary.get('ProfitFactor', 0.0)
        oos_sr = oos_summary.get('SharpeRatio', 0.0)
        oos_trades = oos_summary.get('TotalTrades', 0)
        logging.info(f"OOS Result: PF={oos_pf:.2f}, SR={oos_sr:.2f}, Trades={oos_trades}")

        if _is_oos_passed(oos_summary):
            logging.info(f"OOS validation PASSED for attempt #{i+1}.")
            _save_best_parameters(candidate['params'])
            return True
        else:
            fail_reason = _get_oos_fail_reason(oos_summary)
            log_message = f"OOS validation FAILED for attempt #{i+1} ({fail_reason})."

            # Early stopping logic for OOS validation
            if oos_sr < early_stop_threshold:
                early_stop_trigger_count += 1
                log_message += f" SR below early stop threshold. Trigger: {early_stop_trigger_count}/{config.EARLY_STOP_COUNT}"
            else:
                early_stop_trigger_count = 0 # Reset counter on a non-terrible result

            logging.warning(log_message)

            if early_stop_trigger_count >= config.EARLY_STOP_COUNT:
                logging.error("Early stopping triggered due to consecutively poor OOS performance.")
                break

    logging.error("OOS validation failed for all attempted parameter sets.")
    return False

def _is_oos_passed(oos_summary: dict) -> bool:
    """Checks if the OOS simulation summary meets the minimum passing criteria."""
    return (
        oos_summary.get('TotalTrades', 0) >= config.OOS_MIN_TRADES and
        oos_summary.get('ProfitFactor', 0.0) >= config.OOS_MIN_PROFIT_FACTOR and
        oos_summary.get('SharpeRatio', 0.0) >= config.OOS_MIN_SHARPE_RATIO
    )

def _get_oos_fail_reason(oos_summary: dict) -> str:
    """Generates a string explaining why OOS validation failed."""
    reasons = []
    if oos_summary.get('TotalTrades', 0) < config.OOS_MIN_TRADES:
        reasons.append(f"trades < {config.OOS_MIN_TRADES}")
    if oos_summary.get('ProfitFactor', 0.0) < config.OOS_MIN_PROFIT_FACTOR:
        reasons.append(f"PF < {config.OOS_MIN_PROFIT_FACTOR}")
    if oos_summary.get('SharpeRatio', 0.0) < config.OOS_MIN_SHARPE_RATIO:
        reasons.append(f"SR < {config.OOS_MIN_SHARPE_RATIO}")
    return ", ".join(reasons) if reasons else "Unknown reason"

def _save_best_parameters(params: dict):
    """Renders and saves the final trade configuration file."""
    try:
        with open(config.CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())
        config_str = template.render(params)
        with open(config.BEST_CONFIG_OUTPUT_PATH, 'w') as f:
            f.write(config_str)
        logging.info(f"Successfully updated trade config: {config.BEST_CONFIG_OUTPUT_PATH}")
    except Exception as e:
        logging.error(f"Failed to save the best parameter config file: {e}")
