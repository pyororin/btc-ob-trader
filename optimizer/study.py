import numpy as np
import optuna
import logging
import subprocess
import json
import datetime
from pathlib import Path
from jinja2 import Environment, FileSystemLoader
from typing import Union, Optional, Tuple, Dict, Any

from . import config
from .objective import Objective
from .simulation import run_simulation
from .utils import nest_params, finalize_for_yaml
from .sampler import KDESampler


def create_study(storage_path: str, study_name: str, sampler: Optional[optuna.samplers.BaseSampler] = None) -> optuna.Study:
    """
    Creates and configures a new Optuna study for single-objective optimization.
    The objective is to maximize the penalized Sharpe Ratio.
    """
    logging.info(f"Creating new single-objective Optuna study: {study_name} at {storage_path}")

    # Pruner to stop unpromising trials early.
    pruner = optuna.pruners.HyperbandPruner(min_resource=5, max_resource=100, reduction_factor=3)

    # Use TPESampler for single-objective optimization unless a specific sampler is provided.
    if sampler is None:
        sampler = optuna.samplers.TPESampler(seed=42, n_startup_trials=20)

    return optuna.create_study(
        study_name=study_name,
        storage=storage_path,
        direction='maximize',
        load_if_exists=False,
        pruner=pruner,
        sampler=sampler
    )

from .batch_simulation import BatchSimulator
from .utils import nest_params

def _run_batch_optimization(study: optuna.Study, is_csv_path: Path, n_trials: int):
    """
    Runs a phase of optimization in batch mode using the simulation server.
    Handles both coarse and fine (with stability analysis) phases.
    """
    logging.info(f"Starting batch optimization with {is_csv_path} for {n_trials} trials.")
    phase_name = study.user_attrs.get('phase_name', 'unknown')
    obj_instance = Objective(study) # Reusable instance

    simulator = BatchSimulator(is_csv_path)
    try:
        simulator.start_server()

        # 1. Ask Optuna for all trial parameters
        trials = [study.ask() for _ in range(n_trials)]

        # 2. Prepare and run the main batch request
        main_batch_requests = []
        for trial in trials:
            flat_params = obj_instance._suggest_parameters(trial)
            serializable_params = {k: (int(v) if isinstance(v, np.integer) else float(v) if isinstance(v, np.floating) else bool(v) if isinstance(v, np.bool_) else v) for k, v in flat_params.items()}
            trial.set_user_attr("params_flat", serializable_params)
            main_batch_requests.append({
                "trial_id": trial.number,
                "trade_config": nest_params(serializable_params)
            })
        main_results = simulator.run_batch(main_batch_requests)
        main_results_map = {res['trial_id']: res for res in main_results}

        # 3. If in fine phase, prepare and run a second, larger batch for stability analysis
        jitter_results_map = {}
        if phase_name != 'coarse-phase':
            logging.info(f"Fine phase: Preparing {len(trials) * config.STABILITY_CHECK_N_RUNS} jittered simulations for stability analysis.")
            jitter_batch_requests = []
            for trial in trials:
                # Only create jittered runs for trials that succeeded in the main batch
                if trial.number in main_results_map and not main_results_map[trial.number].get("error"):
                    for i in range(config.STABILITY_CHECK_N_RUNS):
                        jittered_params = obj_instance._get_jittered_params(trial)
                        jitter_batch_requests.append({
                            # Use a unique ID to map back to the original trial and jitter run index
                            "trial_id": f"{trial.number}_jitter_{i}",
                            "trade_config": nest_params(jittered_params)
                        })

            if jitter_batch_requests:
                jitter_results = simulator.run_batch(jitter_batch_requests)
                jitter_results_map = {res['trial_id']: res for res in jitter_results}

        # 4. Process results and tell Optuna
        for trial in trials:
            main_result = main_results_map.get(trial.number)
            if not main_result or main_result.get("error"):
                study.tell(trial, state=optuna.trial.TrialState.PRUNED)
                continue

            summary = main_result.get("summary", {})
            obj_instance._calculate_and_set_metrics(trial, summary, "") # No stderr in batch mode

            # Collect jitter summaries for this trial
            jitter_summaries = []
            if phase_name != 'coarse-phase':
                for i in range(config.STABILITY_CHECK_N_RUNS):
                    jitter_result = jitter_results_map.get(f"{trial.number}_jitter_{i}")
                    if jitter_result and not jitter_result.get("error") and "summary" in jitter_result:
                        jitter_summaries.append(jitter_result["summary"])

                if len(jitter_summaries) < config.STABILITY_CHECK_N_RUNS / 2:
                    logging.warning(f"Trial {trial.number}: Too few successful jittered runs ({len(jitter_summaries)}). Using original result without penalty.")
                    jitter_summaries = [summary]

            # Calculate the final objective value using the centralized, refactored function
            final_value = obj_instance._calculate_final_penalized_sr(trial, phase_name, summary, jitter_summaries)
            study.tell(trial, final_value)

    finally:
        simulator.stop_server()


def run_optimization(study: optuna.Study, is_csv_path: Path, n_trials: int, storage_path: str) -> optuna.Study:
    """
    Runs the main optimization loop for a given study.
    If Coarse-to-Fine (CTF) is enabled, it runs a two-phase optimization.
    Otherwise, it runs a single-phase optimization.

    Returns:
        The final study object to be used for validation.
    """
    if not config.CTF_ENABLED:
        # Non-CTF mode: run a single optimization and return the original study.
        objective_func = Objective(study)
        study.optimize(objective_func, n_trials=n_trials, n_jobs=-1, callbacks=[progress_callback])
        return study

    # --- Phase 1: Coarse Search (Batch Mode) ---
    logging.info("--- Starting Coarse-to-Fine Optimization: Phase 1 (Coarse Search) ---")
    _run_batch_optimization(study, is_csv_path, config.CTF_COARSE_TRIALS)

    completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("Coarse search phase completed with no successful trials. Skipping fine search.")
        return study # Return original study

    # --- Phase 2: Fine Search ---
    logging.info("--- Starting Coarse-to-Fine Optimization: Phase 2 (Fine Search) ---")

    # Select top trials to build the KDE sampler
    completed_trials.sort(key=lambda t: t.value, reverse=True)
    n_top_trials = int(len(completed_trials) * config.CTF_TOP_TRIALS_QUANTILE_FOR_KDE)
    top_trials = completed_trials[:n_top_trials]

    if not top_trials:
        logging.warning("No top trials found after coarse search. Skipping fine search.")
        return study # Return original study

    logging.info(f"Building KDE sampler from top {len(top_trials)} trials.")
    kde_sampler = KDESampler(coarse_trials=top_trials, seed=42)

    # Create a new study for the fine search phase
    fine_study_name = f"{study.study_name}-fine"
    fine_study = create_study(
        storage_path=storage_path,
        study_name=fine_study_name,
        sampler=kde_sampler
    )

    _run_batch_optimization(fine_study, is_csv_path, config.CTF_FINE_TRIALS)

    # Return the new 'fine_study' so the validation logic uses its results.
    # This is the correct way to handle the two-phase optimization.
    return fine_study

def progress_callback(study: optuna.Study, trial: optuna.Trial, n_trials: int):
    """Callback function to report progress periodically."""
    if trial.number > 0 and trial.number % 100 == 0:
        # Check if a best trial exists before trying to access its value.
        # This prevents an error if all initial trials have failed.
        try:
            best_value_str = f"Best value so far: {study.best_value:.4f}"
        except ValueError:
            best_value_str = "No successful trials yet."

        logging.info(
            f"Trial {trial.number}/{n_trials}: {best_value_str}"
        )

def analyze_and_validate(study: optuna.Study, oos_csv_path: Path, cycle_dir: Path) -> Optional[Dict[str, Any]]:
    """
    Analyzes a completed study, performs OOS validation, and returns the results.
    """
    logging.info("Optimization finished. Analyzing results and performing OOS validation.")
    completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("No trials were completed successfully. Skipping OOS validation.")
        return None

    # For single-objective, the best trial is directly available.
    try:
        best_is_trial = study.best_trial
        logging.info(
            f"Best IS trial: #{best_is_trial.number} "
            f"(Value: {best_is_trial.value:.4f}, SR: {best_is_trial.user_attrs.get('sharpe_ratio', 0.0):.2f})"
        )
    except ValueError:
        logging.warning("Could not find the best trial. Skipping OOS validation.")
        return None

    # Sort all completed trials by their objective value to create a list of candidates
    completed_trials.sort(key=lambda t: t.value, reverse=True)
    candidates = _get_oos_candidates(completed_trials)

    # Perform OOS validation and get the results of the passing candidate
    validation_result = _perform_oos_validation(candidates, oos_csv_path)

    if validation_result:
        logging.info(f"OOS validation PASSED. Saving cycle artifacts.")
        _save_cycle_best_parameters(validation_result['params'], cycle_dir)
        _save_global_best_parameters(validation_result['params'])

        final_summary = {
            "cycle_id": cycle_dir.name,
            "status": "success",
            "best_is_trial_number": best_is_trial.number,
            "best_is_value": best_is_trial.value,
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

def _get_oos_candidates(sorted_trials: list[optuna.trial.Trial]) -> list[dict]:
    """Generates a list of parameter candidates for OOS validation from sorted trials."""
    candidates = []
    for rank, trial in enumerate(sorted_trials):
        if len(candidates) >= config.MAX_RETRY:
            break
        candidates.append({
            'params': trial.params,
            'source': f'is_rank_{rank+1}',
            'trial': trial
        })
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

def _save_cycle_best_parameters(flat_params: dict, cycle_dir: Path):
    """Saves the best parameters for the cycle to a json file."""
    output_path = cycle_dir / 'best_params.json'
    try:
        with open(output_path, 'w') as f:
            json.dump(flat_params, f, indent=4)
        logging.info(f"Successfully saved cycle best parameters to {output_path}")
    except Exception as e:
        logging.error(f"Failed to save cycle best parameters to {output_path}: {e}")


# --- Functions for the legacy daemon mode ---

def _save_global_best_parameters(flat_params: dict):
    """Renders and saves the final trade configuration file for the live bot."""
    try:
        nested_params = nest_params(flat_params)
        env = Environment(
            loader=FileSystemLoader(searchpath=config.PARAMS_DIR),
            finalize=finalize_for_yaml
        )
        template = env.get_template(config.CONFIG_TEMPLATE_PATH.name)
        config_str = template.render(nested_params)

        with open(config.BEST_CONFIG_OUTPUT_PATH, 'w') as f:
            f.write(config_str)
        logging.info(f"Successfully updated global trade config: {config.BEST_CONFIG_OUTPUT_PATH}")
    except Exception as e:
        logging.error(f"Failed to save the best parameter config file: {e}")

from .simulation import run_simulation, run_simulation_in_debug_mode

def analyze_and_validate_for_daemon(study: optuna.Study, oos_csv_path: Path) -> bool:
    """
    Analyzes a study and saves the best config file if OOS validation passes.
    This is the legacy function used by the daemon.
    """
    logging.info("Daemon mode: Analyzing results and performing OOS validation.")
    completed_trials = [t for t in study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("No trials were completed successfully. Skipping OOS validation.")
        return False

    # Sort trials by best value and get candidates for OOS
    completed_trials.sort(key=lambda t: t.value, reverse=True)
    candidates = _get_oos_candidates(completed_trials)

    for i, candidate in enumerate(candidates):
        if i >= config.MAX_RETRY:
            logging.warning(f"Reached max_retry limit of {config.MAX_RETRY}. Stopping OOS validation.")
            break

        is_trial = candidate.get('trial')
        is_metrics = is_trial.user_attrs if is_trial else {}
        is_trades = is_metrics.get('trades', 'N/A')
        is_sr = is_metrics.get('sharpe_ratio', 'N/A')
        is_pf = is_metrics.get('profit_factor', 'N/A')

        logging.info(f"--- Running OOS Validation attempt #{i+1} (source: {candidate['source']}) ---")
        logging.info(
            f"IS metrics for this candidate: "
            f"Trades={is_trades}, SR={is_sr:.2f}, PF={is_pf:.2f}"
        )

        nested_params = nest_params(candidate['params'])
        oos_summary = run_simulation(nested_params, oos_csv_path)

        if not isinstance(oos_summary, dict) or not oos_summary:
            logging.warning("OOS simulation failed or returned empty results.")
            # Run in debug mode to see logs from Go
            run_simulation_in_debug_mode(nested_params, oos_csv_path)
            continue

        logging.info(
            f"OOS Result: PF={oos_summary.get('ProfitFactor', 0.0):.2f}, "
            f"SR={oos_summary.get('SharpeRatio', 0.0):.2f}, "
            f"Trades={oos_summary.get('TotalTrades', 0)}"
        )

        # If OOS validation results in zero trades, run a debug simulation to get more insight.
        if oos_summary.get('TotalTrades', 0) == 0:
            logging.warning("OOS simulation resulted in 0 trades. Running in debug mode to get Go logs...")
            run_simulation_in_debug_mode(nested_params, oos_csv_path)

        if _is_oos_passed(oos_summary):
            logging.info(f"OOS validation PASSED for attempt #{i+1}.")
            _save_global_best_parameters(candidate['params'])
            return True
        else:
            fail_reason = _get_oos_fail_reason(oos_summary)
            logging.warning(f"OOS validation FAILED for attempt #{i+1} ({fail_reason}).")

    logging.error("OOS validation failed for all attempted parameter sets.")
    return False

def warm_start_with_recent_trials(study: optuna.Study, recent_days: int):
    """
    Performs a warm-start by adding recent trials from previous studies.
    Used by the daemon mode.
    """
    if not recent_days or recent_days <= 0:
        logging.info("Warm-start is disabled (recent_days is not set).")
        return

    logging.info(f"Attempting warm-start with trials from the last {recent_days} days.")
    # This implementation is simplified and assumes the main storage is used.
    try:
        all_summaries = optuna.get_all_study_summaries(storage=config.STORAGE_URL)
        # Filter out the current study
        completed_studies = [s for s in all_summaries if s.study_name != study.study_name]

        # Filter out studies that have not started (datetime_start is None)
        started_studies = [s for s in completed_studies if s.datetime_start is not None]
        if not started_studies:
            logging.info("No previous started studies found for warm-start.")
            return

        started_studies.sort(key=lambda s: s.datetime_start, reverse=True)
        latest_study_summary = started_studies[0]

        previous_study = optuna.load_study(
            study_name=latest_study_summary.study_name,
            storage=config.STORAGE_URL
        )

        cutoff_dt = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=recent_days)
        recent_trials = []
        for t in previous_study.trials:
            if t.state != optuna.trial.TrialState.COMPLETE or t.datetime_complete is None:
                continue

            dt_complete = t.datetime_complete
            if dt_complete.tzinfo is None:
                # Optuna can store naive datetimes. Assume UTC.
                dt_complete = dt_complete.replace(tzinfo=datetime.timezone.utc)

            if dt_complete > cutoff_dt:
                recent_trials.append(t)

        if len(recent_trials) > config.WARM_START_MAX_TRIALS:
            recent_trials.sort(key=lambda t: t.datetime_complete, reverse=True)
            recent_trials = recent_trials[:config.WARM_START_MAX_TRIALS]

        if recent_trials:
            logging.info(f"Adding {len(recent_trials)} recent trials to the current study for warm-start.")
            study.add_trials(recent_trials)

    except Exception as e:
        logging.error(f"Could not perform warm-start: {e}", exc_info=False)
