import time
import json
import os
import logging
import sys

from . import config
from . import data
from . import study

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
# Suppress verbose logging from Optuna unless it's a warning or error
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)


def run_optimization_job(job: dict):
    """
    Manages a single, complete optimization job from data export to validation.

    Args:
        job: A dictionary containing the job parameters, such as time windows and severity.
    """
    logging.info(f"Processing optimization job: {job}")

    try:
        # --- 1. Data Export & Validation ---
        is_hours = job.get('window_is_hours', 4) # Default to 4 hours
        oos_hours = job.get('window_oos_hours', 1) # Default to 1 hour
        severity = job.get('severity', 'normal')

        # Adjust n_trials based on the severity of the performance drift
        base_n_trials = config.N_TRIALS
        if severity == 'minor':
            n_trials = int(base_n_trials * 0.75)
        elif severity == 'major':
            n_trials = int(base_n_trials * 0.5)
        else:
            n_trials = base_n_trials

        is_csv_path, oos_csv_path = data.export_and_split_data(
            total_hours=is_hours + oos_hours,
            oos_hours=oos_hours
        )
        if not is_csv_path or not oos_csv_path:
            logging.error("Failed to get data. Aborting optimization run.")
            return

        # --- 2. Setup and Run Optuna Study ---
        optuna_study = study.create_study()
        study.run_optimization(optuna_study, is_csv_path, n_trials)

        # --- 3. Analyze Results and Perform OOS Validation ---
        study.analyze_and_validate(optuna_study, oos_csv_path)

    except Exception as e:
        logging.error(f"An unexpected error occurred during the optimization job: {e}", exc_info=True)


def main_loop(run_once: bool = False):
    """
    The main loop of the optimizer service.

    It continuously checks for a job file and processes it when found.

    Args:
        run_once: If True, the loop will exit after one iteration, regardless
                  of whether a job was found.
    """
    if not config.CONFIG_TEMPLATE_PATH.exists():
        logging.error(f"Trade config template not found at {config.CONFIG_TEMPLATE_PATH}. Exiting.")
        return

    logging.info("Optimizer service started. Waiting for optimization job...")

    while True:
        if config.JOB_FILE.exists():
            logging.info(f"Found job file: {config.JOB_FILE}")
            try:
                with open(config.JOB_FILE, 'r') as f:
                    job = json.load(f)

                run_optimization_job(job)

            except json.JSONDecodeError:
                logging.error(f"Invalid JSON in job file. Deleting {config.JOB_FILE}.")
            except Exception as e:
                logging.error(f"An error occurred while processing job file: {e}", exc_info=True)
            finally:
                # Ensure the job file is removed after processing
                if config.JOB_FILE.exists():
                    os.remove(config.JOB_FILE)
                logging.info("Optimization run complete. Waiting for next job.")

        if run_once:
            logging.info("Run_once flag is set. Exiting main loop.")
            break

        time.sleep(10) # Wait before checking for the job file again

    logging.info("Optimizer service shutting down.")


if __name__ == "__main__":
    # Allows running the optimizer once from the command line for testing
    is_run_once = '--run-once' in sys.argv
    main_loop(run_once=is_run_once)
