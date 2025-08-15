import unittest
from unittest.mock import patch, MagicMock
from pathlib import Path
import time
import threading

from optimizer.proc_manager import SimulationManager
from optimizer import config

class TestSimulationManager(unittest.TestCase):

    def setUp(self):
        self.mock_csv_path = Path("/app/data/test.csv")

    @patch('subprocess.Popen')
    def test_start_and_stop_process(self, mock_popen):
        """
        Test that the SimulationManager starts a process correctly,
        waits for the READY signal, and stops it gracefully.
        """
        # --- Mock Popen ---
        # Mock stdin, stdout, stderr to behave like real streams
        mock_stdin = MagicMock()
        mock_stdout = MagicMock()
        mock_stdout.readline.side_effect = ["line1\n", "READY\n", "line3\n", ""]

        # Make stderr readline block until the test is over
        stderr_event = threading.Event()
        def stderr_readline_side_effect():
            stderr_event.wait(timeout=2) # Block until event is set
            return "" # Then return empty string to end the loop
        mock_stderr = MagicMock()
        mock_stderr.readline.side_effect = stderr_readline_side_effect

        mock_process = MagicMock()
        mock_process.stdin = mock_stdin
        mock_process.stdout = mock_stdout
        mock_process.stderr = mock_stderr
        mock_popen.return_value = mock_process

        # --- Test Start ---
        manager = SimulationManager(csv_path=self.mock_csv_path)
        manager.start()

        # Verify Popen was called with the correct command
        expected_command = [
            str(config.SIMULATION_BINARY_PATH),
            '--serve',
            f'--csv={self.mock_csv_path}',
        ]
        mock_popen.assert_called_once()
        self.assertEqual(mock_popen.call_args[0][0], expected_command)

        self.assertIsNotNone(manager._process)
        self.assertTrue(manager._log_thread.is_alive())

        # --- Test Stop ---
        stderr_event.set() # Unblock the stderr thread
        manager.stop()

        # Verify that stop commands were issued
        mock_stdin.write.assert_called_with("EXIT\n")
        mock_process.terminate.assert_called_once()
        self.assertIsNone(manager._process)

    @patch('subprocess.Popen')
    @patch('time.time')
    def test_ready_timeout(self, mock_time, mock_popen):
        """
        Test that the manager raises a TimeoutError if the READY
        signal is not received.
        """
        # --- Mock Popen ---
        # Mock a process that never sends "READY"
        mock_stdout = MagicMock()
        mock_stdout.readline.return_value = "some other output\n"
        mock_process = MagicMock()
        mock_process.stdout = mock_stdout
        mock_process.stderr = MagicMock()
        mock_process.stderr.readline.return_value = "" # Let stderr thread finish
        mock_popen.return_value = mock_process

        # --- Mock time.time to simulate a timeout ---
        # Provide enough values for start_time, the loop check, and the logger
        mock_time.side_effect = [1.0, 2.0, 62.0, 63.0]

        manager = SimulationManager(csv_path=self.mock_csv_path)
        with self.assertRaises(TimeoutError):
            manager.start()

if __name__ == '__main__':
    unittest.main()
