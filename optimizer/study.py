import optuna
import logging
import subprocess
import json
import datetime
import functools
from pathlib import Path
from jinja2 import Environment, FileSystemLoader
from typing import Union, Optional, Tuple, Dict, Any

from . import config
from .objective import Objective, MetricsCalculator
from .simulation import run_simulation
from .utils import nest_params, finalize_for_yaml

def create_study(storage_path: str, study_name: str) -> optuna.Study:
    """
    Creates and configures a new Optuna study for multi-objective optimization.

    Args:
        storage_path: The path to the SQLite database for the study.
        study_name: The name for the study.

    Returns:
        An optuna.Study object.
    """
    logging.info(f"Creating new multi-objective Optuna study: {study_name} at {storage_path}")

    pruner = optuna.pruners.HyperbandPruner(min_resource=5, max_resource=100, reduction_factor=3)
    sampler = optuna.samplers.NSGAIISampler(seed=42)

    return optuna.create_study(
        study_name=study_name,
        storage=storage_path,
        directions=['maximize', 'maximize', 'minimize'],  # SQN (max), ProfitFactor (max), MaxDD (min)
        load_if_exists=False, # Each cycle should be a fresh study
        pruner=pruner,
        sampler=sampler
    )

def run_optimization(study: optuna.Study, is_csv_path: Path, n_trials: int):
    """
    Runs the main optimization loop for a given study.
    """
    study.set_user_attr('current_csv_path', str(is_csv_path))
    objective_func = Objective(study)

    logging.info(f"Starting In-Sample optimization with {is_csv_path} for {n_trials} trials.")
    callback_with_n_trials = functools.partial(progress_callback, n_trials=n_trials)

    study.optimize(
        objective_func,
        n_trials=n_trials,
        n_jobs=-1,
        show_progress_bar=False,
        catch=(Exception,),
        callbacks=[callback_with_n_trials]
    )

def progress_callback(study: optuna.Study, trial: optuna.Trial, n_trials: int):
    """Callback function to report progress periodically."""
    if trial.number > 0 and trial.number % 100 == 0:
        pareto_front = study.best_trials
        logging.info(
            f"Trial {trial.number}/{n_trials}: "
            f"Pareto front contains {len(pareto_front)} trials."
        )

def analyze_and_validate(study: optuna.Study, oos_csv_path: Path, cycle_dir: Path) -> Optional[Dict[str, Any]]:
    """
    Analyzes a completed study, performs OOS validation, and returns the results.

    Instead of saving a global config, this function finds the best parameters for
    the current cycle, validates them, and returns a summary dictionary.

    Args:
        study: The completed Optuna study.
        oos_csv_path: Path to the Out-of-Sample data CSV file.
        cycle_dir: The directory for saving cycle-specific artifacts.

    Returns:
        A dictionary with the results of the best validated candidate, or None.
    """
    logging.info("Optimization finished. Analyzing results and performing OOS validation.")
    completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("No trials were completed successfully. Skipping OOS validation.")
        return None

    metrics_calculator = MetricsCalculator(config.OBJECTIVE_WEIGHTS, completed_trials)
    scored_trials = sorted(
        [(trial, metrics_calculator.calculate_score(trial)) for trial in completed_trials],
        key=lambda x: x[1],
        reverse=True
    )
    best_is_trial, best_is_score = scored_trials[0]
    logging.info(
        f"Best IS trial by custom objective: #{best_is_trial.number} "
        f"(Score: {best_is_score:.4f}, SR: {best_is_trial.user_attrs.get('sharpe_ratio', 0.0):.2f})"
    )

    candidates = _get_oos_candidates(study, scored_trials)

    # Perform OOS validation and get the results of the passing candidate
    validation_result = _perform_oos_validation(candidates, oos_csv_path)

    if validation_result:
        logging.info(f"OOS validation PASSED. Saving cycle artifacts.")
        # Save the best parameters for this specific cycle
        _save_cycle_best_parameters(validation_result['params'], cycle_dir)

        # Combine IS and OOS results into a final summary for this cycle
        final_summary = {
            "cycle_id": cycle_dir.name,
            "status": "success",
            "best_is_trial_number": best_is_trial.number,
            "best_is_score": best_is_score,
            "is_metrics": best_is_trial.user_attrs,
            "oos_metrics": validation_result['summary'],
            "best_params": validation_result['params'],
            "param_source": validation_result['source']
        }
        return final_summary
    else:
        logging.error("OOS validation failed for all attempted parameter sets for this cycle.")
        return {
            "cycle_id": cycle_dir.name,
            "status": "failure",
            "reason": "No parameter set passed OOS validation.",
            "best_is_trial_number": best_is_trial.number,
            "is_metrics": best_is_trial.user_attrs,
        }

def _get_oos_candidates(study: optuna.Study, scored_trials: list) -> list[dict]:
    """Generates a list of parameter candidates for OOS validation."""
    candidates = []
    # Note: The analyzer subprocess logic is removed for simplicity in the WFO context.
    # It could be added back if needed, but top IS trials are a robust starting point.

    # Candidates are the top N trials from IS optimization
    for rank, (trial, score) in enumerate(scored_trials):
        if len(candidates) >= config.MAX_RETRY:
             break
        candidates.append({'params': trial.params, 'source': f'is_rank_{rank+1}'})

    return candidates

def _perform_oos_validation(candidates: list, oos_csv_path: Path) -> Optional[Dict[str, Any]]:
    """
    Iteratively validates candidates and returns the results of the first one that passes.

    Returns:
        A dictionary containing the params, summary, and source of the passing candidate, or None.
    """
    for i, candidate in enumerate(candidates):
        logging.info(f"--- Running OOS Validation attempt #{i+1} (source: {candidate['source']}) ---")
        nested_params = nest_params(candidate['params'])
        oos_summary = run_simulation(nested_params, oos_csv_path)

        if not isinstance(oos_summary, dict) or not oos_summary:
            logging.warning("OOS simulation failed or returned empty results.")
            continue

        logging.info(
            f"OOS Result: PF={oos_summary.get('ProfitFactor', 0.0):.2f}, "
            f"SR={oos_summary.get('SharpeRatio', 0.0):.2f}, "
            f"Trades={oos_summary.get('TotalTrades', 0)}"
        )

        if _is_oos_passed(oos_summary):
            return {
                'params': candidate['params'],
                'summary': oos_summary,
                'source': candidate['source']
            }
        else:
            fail_reason = _get_oos_fail_reason(oos_summary)
            logging.warning(f"OOS validation FAILED for attempt #{i+1} ({fail_reason}).")

    return None

def _is_oos_passed(oos_summary: dict) -> bool:
    """Checks if the OOS simulation summary meets the minimum passing criteria."""
    return (
        oos_summary.get('TotalTrades', 0) >= config.OOS_MIN_TRADES and
        oos_summary.get('SharpeRatio', 0.0) >= config.OOS_MIN_SHARpe_RATIO
    )

def _get_oos_fail_reason(oos_summary: dict) -> str:
    """Generates a string explaining why OOS validation failed."""
    reasons = []
    if oos_summary.get('TotalTrades', 0) < config.OOS_MIN_TRADES:
        reasons.append(f"trades < {config.OOS_MIN_TRADES}")
    if oos_summary.get('SharpeRatio', 0.0) < config.OOS_MIN_SHARPE_RATIO:
        reasons.append(f"SR < {config.OOS_MIN_SHARPE_RATIO}")
    return ", ".join(reasons) if reasons else "Unknown reason"

def _save_cycle_best_parameters(flat_params: dict, cycle_dir: Path):
    """Saves the best parameters for the cycle to a json file."""
    output_path = cycle_dir / 'best_params.json'
    try:
        with open(output_path, 'w') as f:
            json.dump(flat_params, f, indent=4)
        logging.info(f"Successfully saved cycle best parameters to {output_path}")
    except Exception as e:
        logging.error(f"Failed to save cycle best parameters to {output_path}: {e}")

# The warm_start_with_recent_trials function is removed as each WFO cycle
# should be independent to avoid look-ahead bias from one cycle to the next.
# If warm-starting is desired, it should be done carefully, e.g., only from
# the immediately preceding cycle. For now, we simplify and remove it.
