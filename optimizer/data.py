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
    Handles multiple common timestamp formats, including the one from Go's export script.
    """
    processed_ts = ts_str

    # Heuristic to detect Go-specific format: contains a space and ends with a timezone offset like "-07"
    if ' ' in processed_ts and len(processed_ts) > 6 and (processed_ts[-3] == '+' or processed_ts[-3] == '-') and ':' not in processed_ts[-6:]:
        # Convert "YYYY-MM-DD HH:MM:SS.ffffff-07" to "YYYY-MM-DDTHH:MM:SS.ffffff-07:00"
        processed_ts = processed_ts[:-3] + processed_ts[-3:] + ':00'
        processed_ts = processed_ts.replace(' ', 'T', 1)

    try:
        # Try parsing with the potentially modified string
        return datetime.fromisoformat(processed_ts)
    except ValueError:
        try:
            # If that fails, try parsing the original string, in case the heuristic was wrong
            return datetime.fromisoformat(ts_str)
        except ValueError:
            # Final fallback for other non-ISO formats
            for fmt in (
                '%Y-%m-%d %H:%M:%S.%f%z',
                '%Y-%m-%d %H:%M:%S%z',
                '%Y-%m-%d %H:%M:%S',
            ):
                try:
                    return datetime.strptime(ts_str, fmt)
                except ValueError:
                    continue

    raise ValueError(f"Could not parse timestamp: '{ts_str}' with any known format.")
