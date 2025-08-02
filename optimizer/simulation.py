import subprocess
import json
import os
import tempfile
import logging
from pathlib import Path
from jinja2 import Environment, FileSystemLoader

from . import config

def _finalize_for_yaml(value):
    """
    Custom Jinja2 finalizer to ensure YAML-compatible output.
    Specifically, it converts Python booleans to lowercase 'true'/'false'.
    """
    if isinstance(value, bool):
        return str(value).lower()
    # Ensure None is rendered as an empty string to avoid YAML errors
    return value if value is not None else ''


def run_simulation(params: dict, sim_csv_path: Path) -> dict:
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
        A dictionary containing the simulation summary results. Returns an
        empty dictionary if the simulation fails or the output cannot be parsed.
    """
    temp_config_file = None
    try:
        # 1. Create a temporary config file from the template
        # Setup a Jinja2 environment that uses a custom finalizer for YAML compatibility
        env = Environment(
            loader=FileSystemLoader(searchpath=config.PARAMS_DIR),
            finalize=_finalize_for_yaml
        )
        template = env.get_template(config.CONFIG_TEMPLATE_PATH.name)
        config_yaml_str = template.render(params)


        # Use delete=False, so the file is not deleted when the 'with' block exits.
        # This is crucial for the subprocess to be able to access it.
        with tempfile.NamedTemporaryFile(
            mode='w', delete=False, suffix='.yaml', dir=str(config.PARAMS_DIR)
        ) as temp_f:
            temp_config_file = Path(temp_f.name)
            temp_f.write(config_yaml_str)

        # 2. Construct the command to run the Go simulation
        # Execute the pre-compiled binary for performance.
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
            check=True,
            cwd=config.APP_ROOT
        )

        # 4. Parse the JSON output from stdout
        return json.loads(result.stdout)

    except subprocess.CalledProcessError as e:
        logging.error(f"Simulation failed for config {temp_config_file}: {e.stderr}")
        return {}
    except json.JSONDecodeError as e:
        logging.error(f"Failed to parse simulation output for {temp_config_file}: {e}")
        if 'result' in locals():
            logging.error(f"Received output: {result.stdout}")
        return {}
    except FileNotFoundError:
        logging.error(f"Template file not found at {config.CONFIG_TEMPLATE_PATH}")
        return {}
    finally:
        # 5. Manually clean up the temporary config file
        if temp_config_file and temp_config_file.exists():
            try:
                os.remove(temp_config_file)
            except OSError as e:
                logging.error(f"Failed to remove temporary config file {temp_config_file}: {e}")
