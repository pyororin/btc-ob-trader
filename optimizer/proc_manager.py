import subprocess
import logging
import time
import threading
from pathlib import Path
from typing import IO

from . import config

class SimulationManager:
    """
    Manages a long-running Go simulation process in server mode.
    """
    def __init__(self, csv_path: Path):
        self._csv_path = csv_path
        self._process: subprocess.Popen | None = None
        self._log_thread: threading.Thread | None = None

    def start(self):
        """Starts the Go simulation process and waits for it to be ready."""
        if self._process:
            logging.warning("SimulationManager: Process is already running.")
            return

        command = [
            str(config.SIMULATION_BINARY_PATH),
            '--serve',
            f'--csv={self._csv_path}',
        ]
        logging.info(f"Starting simulation process: {' '.join(command)}")

        try:
            self._process = subprocess.Popen(
                command,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                cwd=config.APP_ROOT,
                bufsize=1
            )

            self._log_thread = threading.Thread(target=self._log_stderr, daemon=True)
            self._log_thread.start()

            self._wait_for_ready()

        except FileNotFoundError:
            logging.error(f"Could not find the simulation executable at '{config.SIMULATION_BINARY_PATH}'.")
            raise
        except Exception as e:
            logging.error(f"Failed to start simulation process: {e}")
            self.stop()
            raise

    def stop(self):
        """Stops the simulation process and cleans up resources."""
        if self._process:
            logging.info("Stopping simulation process...")
            if self._process.stdin:
                try:
                    self._process.stdin.write("EXIT\n")
                    self._process.stdin.flush()
                except (IOError, BrokenPipeError) as e:
                     logging.warning(f"Could not write EXIT to simulation process stdin: {e}")

            try:
                self._process.terminate()
                self._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                logging.warning("Process did not terminate gracefully, killing.")
                self._process.kill()
            except Exception as e:
                logging.error(f"An error occurred while stopping the process: {e}")

            self._process = None
            logging.info("Simulation process stopped.")

        if self._log_thread and self._log_thread.is_alive():
            self._log_thread.join(timeout=2)


    def _wait_for_ready(self):
        """Reads stdout until the 'READY' signal is received."""
        if not self._process or not self._process.stdout:
            raise RuntimeError("Process not started or stdout not available.")

        ready_signal = "READY"
        timeout_seconds = 60
        start_time = time.time()

        for line in iter(self._process.stdout.readline, ''):
            if ready_signal in line:
                logging.info("Go simulation process is ready.")
                return
            if time.time() - start_time > timeout_seconds:
                raise TimeoutError(f"Timed out waiting for '{ready_signal}' signal from simulation process.")

        raise RuntimeError("Simulation process terminated unexpectedly before sending READY signal.")

    def _log_stderr(self):
        """Continuously logs the stderr output from the simulation process."""
        if not self._process or not self._process.stderr:
            return
        try:
            for line in iter(self._process.stderr.readline, ''):
                logging.info(f"[GoSim] {line.strip()}")
        except Exception as e:
            logging.debug(f"Stderr logging thread exited: {e}")

    @property
    def stdin(self) -> IO[str] | None:
        return self._process.stdin if self._process else None

    @property
    def stdout(self) -> IO[str] | None:
        return self._process.stdout if self._process else None

    def __enter__(self):
        self.start()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.stop()
