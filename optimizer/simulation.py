import subprocess
import json
import os
import tempfile
import logging
from pathlib import Path
from jinja2 import Environment, FileSystemLoader
import re

from . import config
from .utils import finalize_for_yaml


def run_simulation_in_debug_mode(params: dict, sim_csv_path: Path):
    """
    Runs a Go simulation without JSON output to capture detailed logs for debugging.
    This is intended for cases where a normal simulation yields 0 trades.
    """
    temp_config_file = None
    logging.info(f"--- Starting simulation in DEBUG mode for CSV: {sim_csv_path} ---")
    try:
        # 1. Create a temporary config file
        env = Environment(
            loader=FileSystemLoader(searchpath=config.PARAMS_DIR),
            finalize=finalize_for_yaml
        )
        template = env.get_template(config.CONFIG_TEMPLATE_PATH.name)
        config_yaml_str = template.render(params)

        with tempfile.NamedTemporaryFile(
            mode='w', delete=False, suffix='.yaml', dir=str(config.PARAMS_DIR)
        ) as temp_f:
            temp_config_file = Path(temp_f.name)
            temp_f.write(config_yaml_str)

        # 2. Construct the command to run the Go simulation WITHOUT --json-output
        command = [
            str(config.SIMULATION_BINARY_PATH),
            '--simulate',
            f'--trade-config={temp_config_file}',
            f'--csv={sim_csv_path}',
        ]
        logging.info(f"Executing debug command: {' '.join(command)}")

        # 3. Execute the command
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            check=False,  # Don't raise error on non-zero exit
            cwd=config.APP_ROOT
        )

        # 4. Log the output from stderr, which should contain the Go app's logs
        if result.stderr:
            logging.info("--- Captured Go Simulation Logs (stderr) ---")
            # Log line by line to preserve formatting
            for line in result.stderr.splitlines():
                logging.info(line)
            logging.info("--- End of Captured Go Simulation Logs ---")
        else:
            logging.warning("Debug simulation ran but produced no output on stderr.")

        if result.stdout:
            logging.info("--- Captured Go Simulation Output (stdout) ---")
            for line in result.stdout.splitlines():
                logging.info(line)
            logging.info("--- End of Captured Go Simulation Output ---")

    except Exception as e:
        logging.error(f"An error occurred during the debug simulation run: {e}", exc_info=True)
    finally:
        # 5. Manually clean up the temporary config file
        if temp_config_file and temp_config_file.exists():
            try:
                os.remove(temp_config_file)
            except OSError as e:
                logging.error(f"Failed to remove temporary config file {temp_config_file}: {e}")


def run_simulation(params: dict, sim_csv_path: Path) -> tuple[dict, str]:
    """
    Runs a single Go simulation for a given set of parameters.

    This function takes a dictionary of parameters, renders a temporary trade
    configuration file using a Jinja2 template, and then executes the Go
    simulation binary via a subprocess. It captures and parses the JSON
    output from the simulation.

    Args:
        params: A dictionary of parameters for the trading strategy.
        sim_csv_path: The path to the CSV file containing the market data for the simulation.

    Returns:
        A tuple containing:
        - A dictionary with the simulation summary results.
        - A string with the stderr log output.
        Returns ({}, "") if the simulation fails.
    """
    temp_config_file = None
    try:
        # 1. Create a temporary config file from the template
        env = Environment(
            loader=FileSystemLoader(searchpath=config.PARAMS_DIR),
            finalize=finalize_for_yaml
        )
        template = env.get_template(config.CONFIG_TEMPLATE_PATH.name)
        config_yaml_str = template.render(params)

        with tempfile.NamedTemporaryFile(
            mode='w', delete=False, suffix='.yaml', dir=str(config.PARAMS_DIR)
        ) as temp_f:
            temp_config_file = Path(temp_f.name)
            temp_f.write(config_yaml_str)

        # 2. Construct the command to run the Go simulation
        command = [
            str(config.SIMULATION_BINARY_PATH),
            '--simulate',
            f'--trade-config={temp_config_file}',
            f'--csv={sim_csv_path}',
            '--json-output'
        ]

        # 3. Execute the command
        result = subprocess.run(
            command,
            capture_output=True,
            text=True,
            check=False,  # Set to False to handle non-zero exit codes manually
            cwd=config.APP_ROOT
        )

        # 4. Check for errors
        if result.returncode != 0:
            logging.warning(f"Simulation for {temp_config_file} exited with code {result.returncode}. Stderr: {result.stderr}")
            # Even if it fails, try to parse stdout as it might contain partial results or a valid summary.
            # A common case is a non-zero exit code for simulations with zero trades.

        # 5. Parse the JSON output from stdout
        try:
            summary = json.loads(result.stdout)
        except json.JSONDecodeError:
            # If stdout is not valid JSON, treat it as a failure.
            logging.error(f"Failed to parse simulation JSON output for {temp_config_file}.")
            logging.error(f"Received stdout: {result.stdout}")
            logging.error(f"Received stderr: {result.stderr}")
            return {}, result.stderr  # Return empty dict and the logs

        return summary, result.stderr

    except FileNotFoundError:
        logging.error(f"Template file not found at {config.CONFIG_TEMPLATE_PATH}")
        return {}, ""
    except Exception as e:
        logging.error(f"An unexpected error occurred in run_simulation: {e}", exc_info=True)
        return {}, "" # Return empty results on unexpected errors
    finally:
        # 6. Manually clean up the temporary config file
        if temp_config_file and temp_config_file.exists():
            try:
                os.remove(temp_config_file)
            except OSError as e:
                logging.error(f"Failed to remove temporary config file {temp_config_file}: {e}")
