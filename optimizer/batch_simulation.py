import subprocess
import json
import logging
from pathlib import Path
from typing import List, Dict, Any

from . import config

class BatchSimulator:
    """
    Manages a long-running Go simulation process in server mode to efficiently
    run multiple simulation trials in a single batch.
    """
    def __init__(self, sim_csv_path: Path):
        self.sim_csv_path = sim_csv_path
        self.process = None

    def start_server(self):
        """Starts the Go simulation process in server mode."""
        if self.process:
            logging.warning("Server process is already running.")
            return

        command = [
            str(config.SIMULATION_BINARY_PATH),
            '--serve',
            '--json-output' # Ensure logs are suppressed and only JSON is on stdout
        ]
        logging.info(f"Starting simulation server: {' '.join(command)}")
        try:
            self.process = subprocess.Popen(
                command,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                cwd=config.APP_ROOT
            )
            # Wait for the "READY" signal from the Go server
            ready_line = self.process.stdout.readline()
            if "READY" not in ready_line:
                stderr = self.process.stderr.read()
                raise RuntimeError(f"Simulation server failed to start. Stdout: {ready_line}. Stderr: {stderr}")
            logging.info("Simulation server is READY.")
        except Exception as e:
            logging.error(f"Failed to start simulation server: {e}")
            self.stop_server()
            raise

    def run_batch(self, trials_params: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """
        Sends a batch of simulation requests to the running server and gets results.

        Args:
            trials_params: A list of dictionaries, where each dictionary
                           contains the 'trial_id' and 'trade_config' for a simulation.

        Returns:
            A list of simulation result dictionaries.
        """
        if not self.process:
            raise RuntimeError("Server is not running. Call start_server() first.")

        request = {
            "csv_path": str(self.sim_csv_path),
            "simulations": trials_params
        }
        request_json = json.dumps(request)

        try:
            logging.info(f"Sending batch of {len(trials_params)} trials to simulation server.")
            self.process.stdin.write(request_json + '\n')
            self.process.stdin.flush()

            # Read the single line of JSON output containing all results
            output_line = self.process.stdout.readline()
            if not output_line:
                stderr = self.process.stderr.read()
                raise RuntimeError(f"Simulation server returned no output. Stderr: {stderr}")

            results = json.loads(output_line)
            if "error" in results:
                raise RuntimeError(f"Simulation server returned an error: {results['error']}")

            logging.info(f"Received results for {len(results)} trials from server.")
            return results

        except (BrokenPipeError, json.JSONDecodeError, RuntimeError) as e:
            logging.error(f"An error occurred during batch simulation: {e}")
            # Try to get more context from stderr
            stderr = self.process.stderr.read()
            logging.error(f"Simulation server stderr: {stderr}")
            self.stop_server() # Stop the server on error
            return []

    def stop_server(self):
        """Stops the simulation server process."""
        if self.process:
            logging.info("Stopping simulation server...")
            if self.process.stdin:
                try:
                    # Closing stdin signals EOF to the Go process, allowing it to exit its read loop.
                    self.process.stdin.close()
                except BrokenPipeError:
                    logging.warning("Pipe to simulation server was already closed.")

            try:
                # Wait for the process to terminate gracefully
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                logging.warning("Simulation server did not terminate gracefully, killing.")
                self.process.kill()
                # A final wait after killing to clean up zombie process
                self.process.wait()

            self.process = None
            logging.info("Simulation server stopped.")
