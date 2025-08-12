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
from . import walk_forward

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)


# --- Daemon Mode ---

def run_daemon_job(job: dict):
    """
    Manages a single, complete optimization job triggered by the drift monitor.
    This now uses the Walk-Forward Analysis (WFA) framework instead of a
    simple IS/OOS split.
    """
    logging.info(f"Processing daemon job with Walk-Forward Analysis: {job}")

    try:
        # The daemon job now triggers a full WFA run.
        # The WFA module handles its own data fetching, optimization, and validation.
        wfa_passed = walk_forward.run_walk_forward_analysis(job)

        if wfa_passed:
            logging.info("WFA concluded successfully and the global parameters have been updated.")
        else:
            logging.warning("WFA concluded with a failure. Global parameters were not updated.")

    except Exception as e:
        logging.error(f"An unexpected error occurred during the WFA daemon job: {e}", exc_info=True)


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
    logging.info("No WFO arguments detected. Running in Daemon Mode.")
    main_loop()


if __name__ == "__main__":
    main()
