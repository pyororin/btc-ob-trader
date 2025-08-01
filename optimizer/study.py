import optuna
import logging
import subprocess
import json
import datetime
import functools
from pathlib import Path
from jinja2 import Template
from typing import Union

from . import config
from .objective import Objective, MetricsCalculator
from .simulation import run_simulation

def create_study() -> optuna.Study:
    """
    Creates and configures a new Optuna study for multi-objective optimization.

    It uses a HyperbandPruner for efficient early stopping and the MOTPESampler,
    which is designed for multi-objective problems.

    Returns:
        An optuna.Study object.
    """
    study_name = f"obi-scalp-optimization-{int(datetime.datetime.now().timestamp())}"
    logging.info(f"Creating new multi-objective Optuna study: {study_name}")

    pruner = optuna.pruners.HyperbandPruner(min_resource=5, max_resource=100, reduction_factor=3)
    # NSGAIISampler is the recommended sampler for multi-objective optimization.
    # It is more robust against large Pareto fronts than the deprecated MOTPESampler.
    sampler = optuna.samplers.NSGAIISampler(seed=42)

    return optuna.create_study(
        study_name=study_name,
        storage=config.STORAGE_URL,
        directions=['maximize', 'maximize', 'minimize'],  # SR (max), WinRate (max), MaxDD (min)
        load_if_exists=True,
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

    # Use functools.partial to pass n_trials to the callback
    callback_with_n_trials = functools.partial(progress_callback, n_trials=n_trials)

    study.optimize(
        objective_func,
        n_trials=n_trials,
        n_jobs=-1,  # Use all available CPU cores
        show_progress_bar=False, # Logging callback is used instead
        catch=(Exception,), # Catch all exceptions to prevent optimizer crash
        callbacks=[callback_with_n_trials]
    )

def progress_callback(study: optuna.Study, trial: optuna.Trial, n_trials: int):
    """Callback function to report progress periodically."""
    if trial.number > 0 and trial.number % 100 == 0:
        # In multi-objective optimization, study.best_trial is deprecated.
        # We log the pareto front size or just the trial number.
        pareto_front = study.best_trials
        logging.info(
            f"Trial {trial.number}/{n_trials}: "
            f"Pareto front contains {len(pareto_front)} trials."
        )

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
        analyzer_command = ['python3', '-m', 'optimizer.analyzer', '--study-name', study.study_name]
        result = subprocess.run(
            analyzer_command,
            capture_output=True,
            text=True,
            check=True,
            cwd=config.APP_ROOT
        )
        robust_params = json.loads(result.stdout)
        candidates.append({'params': robust_params, 'source': 'analyzer'})
        logging.info("Analyzer recommended robust parameters as first candidate.")
    except (subprocess.CalledProcessError, json.JSONDecodeError) as e:
        stderr = e.stderr if isinstance(e, subprocess.CalledProcessError) else "JSONDecodeError"
        logging.warning(f"Parameter analyzer failed. Stderr: {stderr}. Proceeding with top IS trials only.")

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

        # Ensure boolean parameters are converted to strings ('true'/'false') for Jinja2 rendering.
        # This handles params from both Optuna (bool) and the analyzer (which are loaded from JSON).
        processed_params = {
            k: str(v).lower() if isinstance(v, bool) else v
            for k, v in candidate['params'].items()
        }
        oos_summary = run_simulation(processed_params, oos_csv_path)

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
        oos_summary.get('SharpeRatio', 0.0) >= config.OOS_MIN_SHARPE_RATIO
    )

def _get_oos_fail_reason(oos_summary: dict) -> str:
    """Generates a string explaining why OOS validation failed."""
    reasons = []
    if oos_summary.get('TotalTrades', 0) < config.OOS_MIN_TRADES:
        reasons.append(f"trades < {config.OOS_MIN_TRADES}")
    if oos_summary.get('SharpeRatio', 0.0) < config.OOS_MIN_SHARPE_RATIO:
        reasons.append(f"SR < {config.OOS_MIN_SHARPE_RATIO}")
    return ", ".join(reasons) if reasons else "Unknown reason"

def _save_best_parameters(params: dict):
    """Renders and saves the final trade configuration file."""
    try:
        # Ensure boolean parameters are correctly formatted as strings for the final config.
        processed_params = {
            k: str(v).lower() if isinstance(v, bool) else v
            for k, v in params.items()
        }
        with open(config.CONFIG_TEMPLATE_PATH, 'r') as f:
            template = Template(f.read())
        config_str = template.render(processed_params)
        with open(config.BEST_CONFIG_OUTPUT_PATH, 'w') as f:
            f.write(config_str)
        logging.info(f"Successfully updated trade config: {config.BEST_CONFIG_OUTPUT_PATH}")
    except Exception as e:
        logging.error(f"Failed to save the best parameter config file: {e}")


def warm_start_with_recent_trials(study: optuna.Study, recent_days: int, n_trials_for_this_run: int):
    """
    Performs a warm-start by adding recent trials from previous studies.

    This function scans the Optuna storage for all completed studies, finds the
    most recent one (excluding the current study), and adds its trials from
    the last `recent_days` to the current study. This helps the sampler
    start with a better prior, especially in a rolling optimization setup.

    Args:
        study: The current Optuna study object to add trials to.
        recent_days: The number of days to look back for recent trials.
    """
    if not recent_days or recent_days <= 0:
        logging.info("Warm-start is disabled (recent_days is not set).")
        return

    logging.info(f"Attempting warm-start with trials from the last {recent_days} days.")

    try:
        all_summaries = optuna.get_all_study_summaries(storage=config.STORAGE_URL)
    except Exception as e:
        logging.error(f"Could not retrieve study summaries for warm-start: {e}")
        return

    # Filter out the current study and find the most recent completed study.
    # The study name format is f"obi-scalp-optimization-{timestamp}".
    completed_studies = [s for s in all_summaries if s.study_name != study.study_name]
    if not completed_studies:
        logging.info("No previous studies found for warm-start.")
        return

    # Sort by the timestamp in the name to find the most recent study.
    try:
        completed_studies.sort(key=lambda s: int(s.study_name.split('-')[-1]), reverse=True)
    except (ValueError, IndexError):
        logging.warning("Could not sort studies by name to find the most recent one. Skipping warm-start.")
        return

    latest_study_summary = completed_studies[0]

    logging.info(f"Loading most recent study '{latest_study_summary.study_name}' for warm-start.")
    try:
        previous_study = optuna.load_study(
            study_name=latest_study_summary.study_name,
            storage=config.STORAGE_URL
        )
    except KeyError:
        logging.warning(f"Failed to load study '{latest_study_summary.study_name}'. Skipping warm-start.")
        return

    # Filter trials from the last `recent_days`.
    # Trial.datetime_complete is a timezone-aware datetime object (UTC).
    cutoff_dt = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=recent_days)

    recent_trials = []
    for t in previous_study.trials:
        if t.state != optuna.trial.TrialState.COMPLETE or t.datetime_complete is None:
            continue

        dt_complete = t.datetime_complete
        # If the datetime object from the database is naive, make it timezone-aware (assume UTC)
        if dt_complete.tzinfo is None:
            dt_complete = dt_complete.replace(tzinfo=datetime.timezone.utc)

        if dt_complete > cutoff_dt:
            recent_trials.append(t)

    if not recent_trials:
        logging.info(f"No recent trials (last {recent_days} days) found in study '{previous_study.study_name}'.")
        return

    # Sort trials by completion time (most recent first)
    recent_trials.sort(key=lambda t: t.datetime_complete, reverse=True)

    # Limit the number of trials to a multiple of the current run's n_trials
    max_warm_start_trials = int(n_trials_for_this_run * config.WARM_START_TRIALS_MULTIPLIER)
    if len(recent_trials) > max_warm_start_trials:
        logging.info(
            f"Limiting warm-start trials from {len(recent_trials)} to the {max_warm_start_trials} most recent ones."
        )
        recent_trials = recent_trials[:max_warm_start_trials]

    # Add the filtered trials to the current study, converting them if necessary.
    logging.info(f"Adding {len(recent_trials)} recent trials to the current study for warm-start.")

    n_objectives_current = len(study.directions)

    for trial in recent_trials:
        try:
            n_objectives_past = len(trial.values)

            if n_objectives_current == n_objectives_past:
                # If the number of objectives is the same, add the trial directly.
                study.add_trial(trial)
            elif n_objectives_past == 3 and n_objectives_current == 2:
                # Handle conversion from 3 objectives (SR, WinRate, MaxDD) to 2 (SR, MaxDD).
                new_values = [trial.values[0], trial.values[2]]
                recreated_trial = optuna.create_trial(
                    state=trial.state,
                    values=new_values,
                    params=trial.params,
                    distributions=trial.distributions,
                    user_attrs=trial.user_attrs,
                    system_attrs=trial.system_attrs,
                    intermediate_values=trial.intermediate_values
                )
                study.add_trial(recreated_trial)
            elif n_objectives_past == 2 and n_objectives_current == 3:
                # Handle conversion from 2 objectives (SR, MaxDD) to 3 (SR, WinRate, MaxDD).
                win_rate = trial.user_attrs.get('win_rate', 0.0)
                new_values = [trial.values[0], win_rate, trial.values[1]]
                recreated_trial = optuna.create_trial(
                    state=trial.state,
                    values=new_values,
                    params=trial.params,
                    distributions=trial.distributions,
                    user_attrs=trial.user_attrs,
                    system_attrs=trial.system_attrs,
                    intermediate_values=trial.intermediate_values
                )
                study.add_trial(recreated_trial)
            else:
                logging.warning(
                    f"Skipping trial #{trial.number} from study '{previous_study.study_name}' due to "
                    f"unhandled objective count mismatch: past={n_objectives_past}, current={n_objectives_current}."
                )

        except Exception as e:
            # This could happen for various reasons, e.g., if a trial somehow gets duplicated.
            logging.error(f"Failed to add trial #{trial.number} for warm-start: {e}", exc_info=False)
