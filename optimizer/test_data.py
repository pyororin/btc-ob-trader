import unittest
from datetime import datetime, timezone, timedelta
from .data import _parse_timestamp

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
