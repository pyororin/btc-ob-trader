import logging
import datetime
import shutil
from pathlib import Path
import pandas as pd
import optuna

from . import study
from . import config
from . import data
from .utils import nest_params
from .simulation import run_simulation


def run_walk_forward_analysis(job: dict) -> bool:
    """
    Orchestrates a full Walk-Forward Analysis.
    This replaces the simple IS/OOS validation in the daemon mode.

    Args:
        job: A dictionary containing job parameters (e.g., n_trials).

    Returns:
        True if the WFA passes and the parameters should be updated, False otherwise.
    """
    logging.info("--- Starting Walk-Forward Analysis ---")
    n_trials = job.get('n_trials_per_fold', config.WFA_N_TRIALS_PER_FOLD)

    # 1. Generate date ranges for each fold
    folds = _generate_wfa_folds()
    if not folds:
        logging.error("Could not generate any folds for WFA. Aborting.")
        return False
    logging.info(f"Generated {len(folds)} folds for WFA.")

    all_fold_results = []
    wfa_main_dir = config.WFA_DIR / f"wfa_{datetime.datetime.utcnow().strftime('%Y%m%d_%H%M%S')}"
    wfa_main_dir.mkdir(parents=True, exist_ok=True)
    logging.info(f"WFA results will be stored in: {wfa_main_dir}")

    # 2. Iterate through each fold
    for i, fold_times in enumerate(folds):
        fold_id = f"fold_{i+1:02d}"
        logging.info(f"--- Processing WFA Fold {i+1}/{len(folds)} ({fold_id}) ---")
        fold_dir = wfa_main_dir / fold_id

        try:
            # a. Export training (IS) and validation (OOS) data for the fold
            train_csv, validate_csv = data.export_and_split_data(
                is_start_time=fold_times['train_start'].isoformat(),
                is_end_time=fold_times['train_end'].isoformat(),
                oos_end_time=fold_times['validate_end'].isoformat(),
                cycle_dir=fold_dir
            )
            if not train_csv or not validate_csv:
                raise ValueError("Data export for fold failed.")

            # b. Create and run an Optuna study on the training data
            storage_path = f"sqlite:///{fold_dir / 'optuna-study.db'}"
            fold_study = study.create_study(storage_path=storage_path, study_name=fold_id)
            study.run_optimization(fold_study, train_csv, n_trials, storage_path)

            # c. Take the best parameters and validate them on the validation data
            fold_result = _validate_fold(fold_study, validate_csv)
            all_fold_results.append(fold_result)

        except Exception as e:
            logging.error(f"Error processing fold {fold_id}: {e}", exc_info=True)
            all_fold_results.append({"status": "failed", "reason": str(e)})

    # 3. Aggregate results and find the best overall parameters
    passed = _aggregate_and_decide(all_fold_results)

    if passed and all_fold_results:
        # Find the parameters from the most recent successful fold
        # Iterate backwards through the results
        for fold_res in reversed(all_fold_results):
            if fold_res.get("status") == "passed":
                best_params = fold_res.get("params")
                if best_params:
                    logging.info(f"WFA PASSED. Saving best parameters from the last successful fold.")
                    study._save_global_best_parameters(best_params)
                    return True

    logging.warning("WFA FAILED. No parameter set passed the aggregate criteria.")
    return False


def _validate_fold(fold_study: optuna.Study, validate_csv: Path) -> dict:
    """
    Takes a completed study from a training fold, finds the best candidate,
    and validates it against the validation dataset.
    """
    completed_trials = [t for t in fold_study.trials if t.state == optuna.trial.TrialState.COMPLETE]
    if not completed_trials:
        logging.warning("No trials completed in this fold. Validation skipped.")
        return {"status": "failed", "reason": "No completed trials."}

    # Find the best trial based on the custom objective score
    metrics_calculator = study.MetricsCalculator(config.OBJECTIVE_WEIGHTS, completed_trials)
    scored_trials = sorted(
        [(trial, metrics_calculator.calculate_score(trial)) for trial in completed_trials],
        key=lambda x: x[1],
        reverse=True
    )
    best_is_trial, _ = scored_trials[0]

    logging.info(f"Best IS trial for fold: #{best_is_trial.number}")

    # Run simulation with the best parameters on the validation data
    validation_summary, _ = run_simulation(nest_params(best_is_trial.params), validate_csv)

    if not validation_summary:
        logging.warning("Validation simulation failed or returned empty results.")
        return {"status": "failed", "reason": "Validation simulation failed.", "is_metrics": best_is_trial.user_attrs}

    # Check if the validation run passed the minimum criteria
    if study._is_oos_passed(validation_summary):
        logging.info("Fold validation PASSED.")
        return {
            "status": "passed",
            "params": best_is_trial.params,
            "is_metrics": best_is_trial.user_attrs,
            "validation_metrics": validation_summary
        }
    else:
        fail_reason = study._get_oos_fail_reason(validation_summary)
        logging.warning(f"Fold validation FAILED: {fail_reason}")
        return {
            "status": "failed",
            "reason": fail_reason,
            "is_metrics": best_is_trial.user_attrs,
            "validation_metrics": validation_summary
        }


def _generate_wfa_folds() -> list[dict]:
    """
    Generates the date ranges for each training and validation split.
    Uses an anchored (or expanding window) approach.
    """
    folds = []
    end_date = datetime.datetime.now(datetime.timezone.utc)

    train_duration = datetime.timedelta(days=config.WFA_TRAIN_DAYS)
    validate_duration = datetime.timedelta(days=config.WFA_VALIDATE_DAYS)
    total_fold_duration = train_duration + validate_duration

    # Ensure total period is not longer than available history
    start_date = end_date - datetime.timedelta(days=config.WFA_TOTAL_DAYS)

    current_train_start = start_date
    for i in range(config.WFA_N_SPLITS):
        train_end = current_train_start + train_duration
        validate_end = train_end + validate_duration

        if validate_end > end_date:
            logging.warning(f"Stopping fold generation as end date {validate_end} exceeds current time.")
            break

        folds.append({
            "train_start": current_train_start,
            "train_end": train_end,
            "validate_start": train_end, # OOS data starts immediately after IS data
            "validate_end": validate_end,
        })

        # For the next fold, we advance by the validation period (anchored WFA)
        current_train_start += validate_duration

    return folds


def _aggregate_and_decide(all_fold_results: list[dict]) -> bool:
    """
    Analyzes the results from all folds to make a final decision.
    """
    if not all_fold_results:
        logging.warning("WFA produced no results to aggregate.")
        return False

    successful_folds = [r for r in all_fold_results if r.get("status") == "passed"]

    success_ratio = len(successful_folds) / len(all_fold_results)
    logging.info(f"WFA Result: {len(successful_folds)}/{len(all_fold_results)} folds passed validation ({success_ratio:.2%}).")

    # The decision rule: at least X% of folds must pass
    return success_ratio >= config.WFA_MIN_SUCCESS_RATIO
