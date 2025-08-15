import json
import logging
import threading
from typing import Dict, Any, Tuple

from .proc_manager import SimulationManager
from .utils import nest_params

# A lock to ensure that only one thread communicates with the simulation process at a time.
# This is crucial because we are using a single, shared simulation process.
process_lock = threading.Lock()

class SimulationRunner:
    """
    Handles running a single simulation trial by communicating with a long-running
    Go simulation process managed by SimulationManager.
    """
    def __init__(self, sim_manager: SimulationManager, trial_id: int):
        """
        Initializes the SimulationRunner.

        Args:
            sim_manager: The SimulationManager instance that holds the running process.
            trial_id: The Optuna trial ID, used for tagging requests.
        """
        self._manager = sim_manager
        self._trial_id = trial_id

    def run(self, flat_params: Dict[str, Any]) -> Tuple[Dict[str, Any], str]:
        """
        Runs a single simulation trial.

        This method serializes the parameters to JSON, sends them to the Go
        process's stdin, and reads the JSON result from its stdout.

        Args:
            flat_params: A flat dictionary of parameters for the trial.

        Returns:
            A tuple containing:
            - A dictionary with the simulation summary results.
            - An empty string (stderr is now logged by SimulationManager's thread).
            Returns ({}, "") if the simulation fails.
        """
        # Ensure that only one thread can write/read to/from the process at a time.
        with process_lock:
            if not self._manager.stdin or not self._manager.stdout:
                logging.error("SimulationRunner: Stdin/Stdout not available.")
                return {}, ""

            try:
                # 1. Nest the flat parameters into the structure the Go app expects.
                nested_params = nest_params(flat_params)

                # 2. Construct the request object.
                #    The Go server expects a list of simulations, but we run one at a time.
                request = {
                    "csv_path": str(self._manager._csv_path), # Pass the CSV path in the request
                    "simulations": [
                        {
                            "trial_id": self._trial_id,
                            "trade_config": nested_params,
                        }
                    ]
                }
                request_json = json.dumps(request)

                # 3. Send the request to the Go process.
                self._manager.stdin.write(request_json + "\n")
                self._manager.stdin.flush()

                # 4. Read the result from the Go process.
                result_line = self._manager.stdout.readline()
                if not result_line:
                    logging.error(f"Trial {self._trial_id}: Did not receive any data from simulation process stdout.")
                    return {}, ""

                # 5. Parse the result. The Go app sends back a list of results.
                response = json.loads(result_line)
                if not isinstance(response, list) or not response:
                    logging.error(f"Trial {self._trial_id}: Invalid or empty response: {response}")
                    return {}, ""

                # We only sent one simulation, so we only care about the first result.
                sim_result = response[0]

                if sim_result.get("error"):
                    logging.error(f"Trial {self._trial_id}: Simulation returned an error: {sim_result['error']}")
                    return {}, ""

                return sim_result.get("summary", {}), ""

            except (IOError, BrokenPipeError) as e:
                logging.error(f"Trial {self._trial_id}: Communication error with simulation process: {e}")
                # The process likely died. The manager should handle restarting it if necessary.
                raise
            except json.JSONDecodeError as e:
                logging.error(f"Trial {self._trial_id}: Failed to decode JSON response: {e}. Response: '{result_line}'")
                return {}, ""
            except Exception as e:
                logging.error(f"Trial {self._trial_id}: An unexpected error occurred in SimulationRunner: {e}", exc_info=True)
                return {}, ""
