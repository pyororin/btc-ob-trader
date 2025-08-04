import logging
import argparse
from pathlib import Path
import json
import datetime

from . import config
from . import data
from . import study

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)


def run_optimization_cycle(
    is_start_time: str,
    is_end_time: str,
    oos_end_time: str,
    cycle_id: str,
    n_trials: int,
):
    """
    Manages a single, complete walk-forward optimization (WFO) cycle.

    This function orchestrates the entire process for one cycle:
    1.  Exports and splits the data for the given time windows.
    2.  Sets up and runs an Optuna study for the In-Sample data.
    3.  Analyzes the results and validates the best parameters on Out-of-Sample data.
    4.  Saves the results of the cycle to a JSON file.

    Args:
        is_start_time: IS window start time string.
        is_end_time: IS window end time string (also the split point).
        oos_end_time: OOS window end time string.
        cycle_id: A unique identifier for this WFO cycle.
        n_trials: The number of optimization trials to run.
    """
    logging.info(f"--- Starting WFO Cycle: {cycle_id} ---")
    logging.info(f"IS Window: {is_start_time} -> {is_end_time}")
    logging.info(f"OOS Window: {is_end_time} -> {oos_end_time}")

    # Define a cycle-specific directory for all artifacts
    cycle_dir = config.WFO_RESULTS_DIR / cycle_id
    cycle_dir.mkdir(parents=True, exist_ok=True)

    try:
        # 1. Data Export & Split for the current cycle
        is_csv_path, oos_csv_path = data.export_and_split_data(
            is_start_time=is_start_time,
            is_end_time=is_end_time,
            oos_end_time=oos_end_time,
            cycle_dir=cycle_dir
        )
        if not is_csv_path or not oos_csv_path:
            logging.error(f"Failed to get data for cycle {cycle_id}. Aborting cycle.")
            # Record failure
            summary = {"cycle_id": cycle_id, "status": "failure", "reason": "Data export/split failed."}
            with open(cycle_dir / "summary.json", 'w') as f:
                json.dump(summary, f, indent=4)
            return

        # 2. Setup and Run Optuna Study
        # Each cycle gets its own study name and database file to ensure isolation
        study_name = f"wfo-cycle-{cycle_id}"
        storage_path = f"sqlite:///{cycle_dir / 'optuna-study.db'}"
        optuna_study = study.create_study(storage_path=storage_path, study_name=study_name)

        study.run_optimization(optuna_study, is_csv_path, n_trials)

        # 3. Analyze Results, Perform OOS Validation, and get the summary
        summary = study.analyze_and_validate(optuna_study, oos_csv_path, cycle_dir)

        # 4. Save the summary of the cycle to a JSON file
        summary_path = cycle_dir / "summary.json"
        with open(summary_path, 'w') as f:
            # Add timestamps to the summary for record-keeping
            summary['cycle_start_time_utc'] = datetime.datetime.utcnow().isoformat()
            json.dump(summary, f, indent=4, default=str) # Use default=str for datetime etc.

        logging.info(f"Successfully saved WFO cycle '{cycle_id}' summary to {summary_path}")

    except Exception as e:
        logging.error(f"An unexpected error occurred during WFO cycle {cycle_id}: {e}", exc_info=True)
        # Record failure
        summary = {"cycle_id": cycle_id, "status": "failure", "reason": str(e)}
        with open(cycle_dir / "summary.json", 'w') as f:
            json.dump(summary, f, indent=4)


def main():
    """
    Main entry point for the WFO cycle runner script.
    Parses command-line arguments and triggers a single optimization cycle.
    """
    parser = argparse.ArgumentParser(description="Run a single WFO optimization cycle.")
    parser.add_argument("--is-start-time", required=True, help="IS window start time (YYYY-MM-DD HH:MM:SS)")
    parser.add_argument("--is-end-time", required=True, help="IS window end time (YYYY-MM-DD HH:MM:SS)")
    parser.add_argument("--oos-end-time", required=True, help="OOS window end time (YYYY-MM-DD HH:MM:SS)")
    parser.add_argument("--cycle-id", required=True, help="Unique identifier for this WFO cycle (e.g., 'cycle-01')")
    parser.add_argument("--n-trials", type=int, default=config.N_TRIALS, help="Number of optimization trials")
    args = parser.parse_args()

    # Basic validation
    if not config.CONFIG_TEMPLATE_PATH.exists():
        logging.error(f"Trade config template not found at {config.CONFIG_TExMPLATE_PATH}. Exiting.")
        return

    run_optimization_cycle(
        is_start_time=args.is_start_time,
        is_end_time=args.is_end_time,
        oos_end_time=args.oos_end_time,
        cycle_id=args.cycle_id,
        n_trials=args.n_trials,
    )


if __name__ == "__main__":
    main()
