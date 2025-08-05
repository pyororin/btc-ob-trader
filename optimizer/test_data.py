import unittest
import os
import shutil
from pathlib import Path
from unittest.mock import patch, MagicMock
from datetime import datetime, timezone, timedelta
from .data import _parse_timestamp, export_and_split_data
from . import config

class TestDataSplitter(unittest.TestCase):
    def setUp(self):
        """Set up a temporary directory and a dummy CSV file for testing."""
        self.test_dir = Path("./test_simulation_dir")
        self.test_dir.mkdir(exist_ok=True)
        # Override the config to use our test directory
        self.config_patcher = patch.object(config, 'SIMULATION_DIR', self.test_dir)
        self.config_patcher.start()

        self.dummy_csv_path = self.test_dir / "order_book_updates_dummy.csv"
        # Create a CSV with 10 lines, 1 minute apart
        with open(self.dummy_csv_path, 'w') as f:
            f.write("time,event_type,pair,side,price,size,is_snapshot,trade_id\n")
            start_time = datetime(2025, 1, 1, 12, 0, 0, tzinfo=timezone.utc)
            for i in range(10):
                ts = start_time + timedelta(minutes=i)
                f.write(f"{ts.isoformat()},book,btc_jpy,buy,5000000,0.1,false,\n")

    def tearDown(self):
        """Clean up the temporary directory."""
        self.config_patcher.stop()
        shutil.rmtree(self.test_dir)

    @patch('optimizer.data._cleanup_directory')
    @patch('optimizer.data.subprocess.run')
    @patch('optimizer.data._find_latest_csv')
    def test_export_and_split_data_with_ceil(self, mock_find_csv, mock_subprocess_run, mock_cleanup):
        """
        Tests that export_and_split_data calls the Go exporter with a ceiled
        hour value and correctly splits the resulting data.
        """
        # --- Mock setup ---
        # Mock _find_latest_csv to return our dummy file
        mock_find_csv.return_value = self.dummy_csv_path
        # Mock subprocess.run to do nothing
        mock_subprocess_run.return_value = MagicMock(returncode=0)

        # --- Call the function with fractional hours ---
        # Total duration is 10 minutes. IS = 8 mins, OOS = 2 mins.
        # Ratio: 8/10 = 0.8
        total_hours = 10 / 60  # 0.1666... hours
        oos_hours = 2 / 60   # 0.0333... hours
        # This test targets the legacy daemon function
        from .data import export_and_split_data_for_daemon
        export_and_split_data_for_daemon(total_hours, oos_hours)

        # --- Assertions ---
        # 1. Assert that the Go exporter was called with the ceiled value (1 hour)
        expected_hours_arg = f"--hours-before={1}" # math.ceil(0.1666...) = 1
        called_command = mock_subprocess_run.call_args[0][0]
        self.assertIn(expected_hours_arg, called_command)

        # 2. Assert that the files were split correctly
        is_path = self.test_dir / "is_data.csv"
        oos_path = self.test_dir / "oos_data.csv"
        self.assertTrue(is_path.exists())
        self.assertTrue(oos_path.exists())

        # The split should be after the 8th data line (index 7)
        # 1 header line + 8 data lines = 9 lines in IS file
        # 1 header line + 2 data lines = 3 lines in OOS file
        with open(is_path, 'r') as f:
            is_lines = f.readlines()
        with open(oos_path, 'r') as f:
            oos_lines = f.readlines()

        self.assertEqual(len(is_lines), 9) # Header + 8 data lines
        self.assertEqual(len(oos_lines), 3) # Header + 2 data lines

        # Check the last line of IS data
        self.assertIn("2025-01-01T12:07:00+00:00", is_lines[-1])
        # Check the first line of OOS data
        self.assertIn("2025-01-01T12:08:00+00:00", oos_lines[1])

class TestTimestampParser(unittest.TestCase):

    def test_parse_standard_iso_format(self):
        """Tests parsing of standard ISO 8601 format."""
        ts_str = "2025-07-30T10:44:44.099860+00:00"
        expected = datetime(2025, 7, 30, 10, 44, 44, 99860, tzinfo=timezone.utc)
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_parse_space_separator_format(self):
        """Tests parsing of ISO 8601 format with a space separator."""
        ts_str = "2025-07-30 10:44:44.099860+00:00"
        expected = datetime(2025, 7, 30, 10, 44, 44, 99860, tzinfo=timezone.utc)
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_parse_no_colon_in_timezone(self):
        """Tests parsing of timestamps with no colon in the timezone offset."""
        ts_str = "2025-07-30 10:44:44.09986+00"
        expected = datetime(2025, 7, 30, 10, 44, 44, 99860, tzinfo=timezone.utc)
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_parse_negative_timezone_offset(self):
        """Tests parsing with a negative timezone offset."""
        ts_str = "2025-07-30T10:44:44.099860-05:00"
        expected = datetime(2025, 7, 30, 10, 44, 44, 99860, tzinfo=timezone(timedelta(hours=-5)))
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_parse_no_microseconds(self):
        """Tests parsing of timestamps without microseconds."""
        ts_str = "2025-07-30 10:44:44+00:00"
        expected = datetime(2025, 7, 30, 10, 44, 44, tzinfo=timezone.utc)
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_parse_naive_timestamp(self):
        """Tests parsing of a naive timestamp (no timezone)."""
        ts_str = "2025-07-30 10:44:44"
        expected = datetime(2025, 7, 30, 10, 44, 44, tzinfo=timezone.utc)
        self.assertEqual(_parse_timestamp(ts_str), expected)

    def test_unsupported_format(self):
        """Tests that an unsupported format raises a ValueError."""
        ts_str = "2025/07/30 10-44-44"
        with self.assertRaises(ValueError):
            _parse_timestamp(ts_str)

if __name__ == '__main__':
    unittest.main()
