import logging
import argparse
from pathlib import Path
import json
import datetime
import time
import os
import sys
import optuna

from . import config
from . import data
from . import study

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)


# --- WFO Cycle Mode ---

def run_wfo_cycle(
    is_start_time: str,
    is_end_time: str,
    oos_end_time: str,
    cycle_id: str,
    n_trials: int,
):
    """
    Manages a single, complete walk-forward optimization (WFO) cycle.
    """
    logging.info(f"--- Starting WFO Cycle: {cycle_id} ---")
    logging.info(f"IS Window: {is_start_time} -> {is_end_time}")
    logging.info(f"OOS Window: {is_end_time} -> {oos_end_time}")

    cycle_dir = config.WFO_RESULTS_DIR / cycle_id
    cycle_dir.mkdir(parents=True, exist_ok=True)

    try:
        is_csv_path, oos_csv_path = data.export_and_split_data(
            is_start_time=is_start_time,
            is_end_time=is_end_time,
            oos_end_time=oos_end_time,
            cycle_dir=cycle_dir
        )
        if not is_csv_path or not oos_csv_path:
            raise ValueError(f"Failed to get data for cycle {cycle_id}.")

        study_name = f"wfo-cycle-{cycle_id}"
        storage_path = f"sqlite:///{cycle_dir / 'optuna-study.db'}"
        optuna_study = study.create_study(storage_path=storage_path, study_name=study_name)

        study.run_optimization(optuna_study, is_csv_path, n_trials)

        summary = study.analyze_and_validate(optuna_study, oos_csv_path, cycle_dir)

        summary_path = cycle_dir / "summary.json"
        with open(summary_path, 'w') as f:
            summary['cycle_start_time_utc'] = datetime.datetime.utcnow().isoformat()
            json.dump(summary, f, indent=4, default=str)

        logging.info(f"Successfully saved WFO cycle '{cycle_id}' summary to {summary_path}")

    except Exception as e:
        logging.error(f"An unexpected error occurred during WFO cycle {cycle_id}: {e}", exc_info=True)
        summary = {"cycle_id": cycle_id, "status": "failure", "reason": str(e)}
        with open(cycle_dir / "summary.json", 'w') as f:
            json.dump(summary, f, indent=4)


# --- Daemon Mode ---

def run_daemon_job(job: dict):
    """
    Manages a single, complete optimization job triggered by the drift monitor.
    """
    logging.info(f"Processing daemon job: {job}")

    try:
        is_hours = job.get('window_is_hours', 4)
        oos_hours = job.get('window_oos_hours', 1)
        n_trials = job.get('n_trials', config.N_TRIALS)

        is_csv_path, oos_csv_path = data.export_and_split_data_for_daemon(
            total_hours=is_hours + oos_hours,
            oos_hours=oos_hours
        )
        if not is_csv_path or not oos_csv_path:
            logging.error("Failed to get data for daemon job. Aborting.")
            return

        # Use the global study database for the daemon
        study_name = f"daemon-opt-{int(datetime.datetime.now().timestamp())}"
        # The create_study function needs to be adapted for daemon use
        # Let's create a separate one or adapt the existing one.
        # For now, let's assume a default global study.
        storage_path = config.STORAGE_URL
        # We need a way to create a study with the global storage.
        # The refactored `create_study` requires a specific storage path.
        # Let's create a wrapper or modify `create_study`.
        # For now, let's just create a study with the default storage URL
        optuna_study = optuna.create_study(
            study_name=study_name,
            storage=storage_path,
            directions=['maximize', 'maximize', 'minimize'],
            load_if_exists=True,
        )

        recent_days = job.get('recent_days_warm_start', 1)
        study.warm_start_with_recent_trials(optuna_study, recent_days)

        study.run_optimization(optuna_study, is_csv_path, n_trials)

        study.analyze_and_validate_for_daemon(optuna_study, oos_csv_path)

    except Exception as e:
        logging.error(f"An unexpected error occurred during the daemon job: {e}", exc_info=True)


def main_loop():
    """
    The main loop of the optimizer service in daemon mode.
    """
    if not config.CONFIG_TEMPLATE_PATH.exists():
        logging.error(f"Trade config template not found at {config.CONFIG_TEMPLATE_PATH}. Exiting.")
        return

    logging.info("Optimizer service started in daemon mode. Waiting for optimization job...")

    while True:
        if config.JOB_FILE.exists():
            logging.info(f"Found job file: {config.JOB_FILE}")
            try:
                with open(config.JOB_FILE, 'r') as f:
                    job = json.load(f)
                run_daemon_job(job)
            except json.JSONDecodeError:
                logging.error(f"Invalid JSON in job file. Deleting {config.JOB_FILE}.")
            except Exception as e:
                logging.error(f"An error occurred while processing job file: {e}", exc_info=True)
            finally:
                if config.JOB_FILE.exists():
                    os.remove(config.JOB_FILE)
                logging.info("Daemon job complete. Waiting for next job.")

        time.sleep(10)


# --- Main Entrypoint ---

def main():
    """
    Main entry point. Determines whether to run in WFO mode or daemon mode.
    """
    # Check if WFO-specific arguments are present
    wfo_args = ['--is-start-time', '--is-end-time', '--oos-end-time', '--cycle-id']
    is_wfo_mode = any(arg in sys.argv for arg in wfo_args)

    if is_wfo_mode:
        logging.info("Running in WFO Cycle Mode.")
        parser = argparse.ArgumentParser(description="Run a single WFO optimization cycle.")
        parser.add_argument("--is-start-time", required=True)
        parser.add_argument("--is-end-time", required=True)
        parser.add_argument("--oos-end-time", required=True)
        parser.add_argument("--cycle-id", required=True)
        parser.add_argument("--n-trials", type=int, default=config.N_TRIALS)
        args = parser.parse_args()

        if not config.CONFIG_TEMPLATE_PATH.exists():
            logging.error(f"Trade config template not found. Exiting.")
            return

        run_wfo_cycle(
            is_start_time=args.is_start_time,
            is_end_time=args.is_end_time,
            oos_end_time=args.oos_end_time,
            cycle_id=args.cycle_id,
            n_trials=args.n_trials,
        )
    else:
        logging.info("No WFO arguments detected. Running in Daemon Mode.")
        main_loop()


if __name__ == "__main__":
    main()
