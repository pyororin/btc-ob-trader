import json
import logging
import threading
from typing import Dict, Any, Tuple

from .proc_manager import SimulationManager
from .utils import nest_params

process_lock = threading.Lock()

class SimulationRunner:
    """
    Handles running a single simulation trial by communicating with a long-running
    Go simulation process managed by SimulationManager.
    """
    def __init__(self, sim_manager: SimulationManager, trial_id: int):
        self._manager = sim_manager
        self._trial_id = trial_id

    def run(self, flat_params: Dict[str, Any]) -> Tuple[Dict[str, Any], str]:
        """
        Runs a single simulation trial.
        """
        with process_lock:
            if not self._manager.stdin or not self._manager.stdout:
                logging.error("SimulationRunner: Stdin/Stdout not available.")
                return {}, ""

            try:
                nested_params = nest_params(flat_params)
                request = {
                    "csv_path": str(self._manager._csv_path),
                    "simulations": [
                        {
                            "trial_id": self._trial_id,
                            "trade_config": nested_params,
                        }
                    ]
                }
                request_json = json.dumps(request)

                self._manager.stdin.write(request_json + "\n")
                self._manager.stdin.flush()

                result_line = self._manager.stdout.readline()
                if not result_line:
                    logging.error(f"Trial {self._trial_id}: Did not receive any data from simulation process stdout.")
                    return {}, ""

                response = json.loads(result_line)
                if not isinstance(response, list) or not response:
                    logging.error(f"Trial {self._trial_id}: Invalid or empty response: {response}")
                    return {}, ""

                sim_result = response[0]

                if sim_result.get("error"):
                    logging.error(f"Trial {self._trial_id}: Simulation returned an error: {sim_result['error']}")
                    return {}, ""

                return sim_result.get("summary", {}), ""

            except (IOError, BrokenPipeError) as e:
                logging.error(f"Trial {self._trial_id}: Communication error with simulation process: {e}")
                raise
            except json.JSONDecodeError as e:
                logging.error(f"Trial {self._trial_id}: Failed to decode JSON response: {e}. Response: '{result_line}'")
                return {}, ""
            except Exception as e:
                logging.error(f"Trial {self._trial_id}: An unexpected error occurred in SimulationRunner: {e}", exc_info=True)
                return {}, ""
