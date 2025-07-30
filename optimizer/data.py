import logging
import subprocess
import shutil
import os
from pathlib import Path
from datetime import datetime, timedelta, timezone
from typing import Union, Tuple

from . import config

def export_and_split_data(total_hours: float, oos_hours: float) -> Tuple[Union[Path, None], Union[Path, None]]:
    """
    Exports data from the database and splits it into In-Sample (IS) and Out-of-Sample (OOS).

    This function orchestrates the data preparation pipeline:
    1. Cleans up the simulation directory.
    2. Calls the Go export script to dump data from TimescaleDB.
    3. Splits the exported data into IS and OOS sets based on the provided time windows.

    Args:
        total_hours: The total duration of data to export, in hours.
        oos_hours: The duration of the trailing data to be used for OOS validation, in hours.

    Returns:
        A tuple containing the paths to the IS and OOS CSV files, respectively.
        Returns (None, None) if any step in the process fails.
    """
    logging.info(f"Exporting data for the last {total_hours} hours...")
    _cleanup_simulation_directory()

    cmd = [
        'go', 'run', 'cmd/export/main.go',
        f'--hours-before={int(total_hours)}',
        '--no-zip',
        f'--trade-config={config.BEST_CONFIG_OUTPUT_PATH}'
    ]

    try:
        subprocess.run(cmd, check=True, capture_output=True, text=True, cwd=config.APP_ROOT)

        full_dataset_path = _find_latest_csv(config.SIMULATION_DIR)
        if not full_dataset_path:
            logging.error("No CSV file found after export.")
            return None, None

        logging.info(f"Successfully exported data to {full_dataset_path}")

        is_path, oos_path = _split_data_by_timestamp(
            full_dataset_path=full_dataset_path,
            total_hours=total_hours,
            oos_hours=oos_hours
        )
        return is_path, oos_path

    except subprocess.CalledProcessError as e:
        logging.error(f"Failed to export data: {e.stderr}")
        return None, None

def _cleanup_simulation_directory():
    """Removes and recreates the simulation directory to ensure a clean state."""
    if config.SIMULATION_DIR.exists():
        shutil.rmtree(config.SIMULATION_DIR)
    config.SIMULATION_DIR.mkdir(parents=True)

def _find_latest_csv(directory: Path) -> Union[Path, None]:
    """Finds the most recently modified CSV file in a directory."""
    exported_files = list(directory.glob("*.csv"))
    if not exported_files:
        return None
    exported_files.sort(key=os.path.getmtime, reverse=True)
    return exported_files[0]

def _split_data_by_timestamp(full_dataset_path: Path, total_hours: float, oos_hours: float) -> tuple[Path, Path]:
    """
    Splits a CSV file into In-Sample (IS) and Out-of-Sample (OOS) sets based on timestamps.
    """
    is_ratio = (total_hours - oos_hours) / total_hours
    is_path = config.SIMULATION_DIR / "is_data.csv"
    oos_path = config.SIMULATION_DIR / "oos_data.csv"

    with open(full_dataset_path, 'r') as f_full:
        lines = f_full.readlines()

    header = lines[0]
    data_lines = lines[1:]

    if not data_lines:
        logging.warning("No data to split. Creating empty IS and OOS files.")
        with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
            f_is.write(header)
            f_oos.write(header)
        return is_path, oos_path

    try:
        first_time = _parse_timestamp(data_lines[0].split(',')[0])
        last_time = _parse_timestamp(data_lines[-1].split(',')[0])

        total_duration_seconds = (last_time - first_time).total_seconds()
        is_duration_seconds = total_duration_seconds * is_ratio
        split_time = first_time + timedelta(seconds=is_duration_seconds)

        with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
            f_is.write(header)
            f_oos.write(header)
            split_found = False
            for line in data_lines:
                current_time = _parse_timestamp(line.split(',')[0])
                if current_time < split_time:
                    f_is.write(line)
                else:
                    f_oos.write(line)
                    split_found = True
            if not split_found:
                logging.warning("Split time was after all data points. OOS will be empty.")

    except (ValueError, IndexError) as e:
        logging.error(f"Timestamp parsing failed: {e}. Falling back to line-based split.")
        split_index = int(len(data_lines) * is_ratio)
        with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
            f_is.write(header)
            f_is.writelines(data_lines[:split_index])
            f_oos.write(header)
            f_oos.writelines(data_lines[split_index:])

    is_lines = len((is_path).read_text().splitlines())
    oos_lines = len((oos_path).read_text().splitlines())
    logging.info(f"Split data into IS ({is_path}, {is_lines} lines) and OOS ({oos_path}, {oos_lines} lines)")

    return is_path, oos_path


def _parse_timestamp(ts_str: str) -> datetime:
    """
    Parses a timestamp string into a timezone-aware datetime object.
    This function is designed to be robust and handle multiple timestamp formats,
    including standard ISO 8601, and non-standard variations from other services.
    """
    original_ts = ts_str.strip()
    logging.debug(f"Attempting to parse timestamp: '{original_ts}'")
    
    # Handle the specific problematic format: "YYYY-MM-DD HH:MM:SS.ffffff+XX"
    # Convert it to proper ISO format first
    if len(original_ts) >= 3 and (original_ts[-3] == '+' or original_ts[-3] == '-') and original_ts[-2:].isdigit():
        # This handles formats like "2025-07-30 10:26:17.53492+00"
        corrected_ts = original_ts + ':00'
        corrected_ts = corrected_ts.replace(' ', 'T', 1)
        logging.debug(f"Pre-correcting timezone format: '{original_ts}' -> '{corrected_ts}'")
        try:
            return datetime.fromisoformat(corrected_ts)
        except ValueError as e:
            logging.debug(f"Pre-corrected format failed: {e}")
    
    # First, try to parse the timestamp directly using the powerful fromisoformat.
    # This handles standard formats like "YYYY-MM-DDTHH:MM:SS.ffffff+ZZ:ZZ" very efficiently.
    try:
        # Replace space with 'T' for broader ISO 8601 compatibility
        iso_str = original_ts.replace(' ', 'T', 1)
        logging.debug(f"Trying fromisoformat with: '{iso_str}'")
        return datetime.fromisoformat(iso_str)
    except ValueError as e:
        logging.debug(f"fromisoformat failed: {e}")
        # If direct parsing fails, proceed to handle known non-standard formats.
        pass

    # Manual parsing for problematic timezone formats
    import re
    
    # Pattern for timestamps with 2-digit timezone: "YYYY-MM-DD HH:MM:SS.ffffff+XX"
    pattern = r'^(\d{4}-\d{2}-\d{2})\s+(\d{2}:\d{2}:\d{2})(?:\.(\d+))?([+-]\d{2})$'
    match = re.match(pattern, original_ts)
    
    if match:
        date_part, time_part, microseconds, tz_part = match.groups()
        
        # Construct proper ISO format
        if microseconds:
            # Pad or truncate microseconds to 6 digits
            microseconds = microseconds.ljust(6, '0')[:6]
            iso_str = f"{date_part}T{time_part}.{microseconds}{tz_part}:00"
        else:
            iso_str = f"{date_part}T{time_part}{tz_part}:00"
        
        logging.debug(f"Manual regex parsing: '{original_ts}' -> '{iso_str}'")
        try:
            return datetime.fromisoformat(iso_str)
        except ValueError as e:
            logging.debug(f"Manual regex parsing failed: {e}")

    # Final fallback for any other formats that strptime can handle.
    formats_to_try = [
        '%Y-%m-%d %H:%M:%S.%f%z',
        '%Y-%m-%d %H:%M:%S%z',
        '%Y-%m-%d %H:%M:%S.%f',
        '%Y-%m-%d %H:%M:%S',
        '%Y-%m-%dT%H:%M:%S.%f%z',
        '%Y-%m-%dT%H:%M:%S%z',
        '%Y-%m-%dT%H:%M:%S.%f',
        '%Y-%m-%dT%H:%M:%S',
    ]
    
    for fmt in formats_to_try:
        try:
            logging.debug(f"Trying strptime with format: '{fmt}'")
            parsed = datetime.strptime(original_ts, fmt)
            # If no timezone info, assume UTC
            if parsed.tzinfo is None:
                parsed = parsed.replace(tzinfo=timezone.utc)
            return parsed
        except ValueError as e:
            logging.debug(f"strptime with '{fmt}' failed: {e}")
            continue

    raise ValueError(f"Could not parse timestamp: '{original_ts}' with any known format.")
