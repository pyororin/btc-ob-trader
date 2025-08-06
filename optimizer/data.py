import logging
import subprocess
import shutil
import os
from pathlib import Path
from datetime import datetime, timezone, timedelta
from typing import Union, Tuple

from . import config

def export_and_split_data(
    is_start_time: str, is_end_time: str, oos_end_time: str, cycle_dir: Path
) -> Tuple[Union[Path, None], Union[Path, None]]:
    """
    Exports data for a specific WFO cycle and splits it into In-Sample and Out-of-Sample sets.

    This function orchestrates the data preparation for a single walk-forward cycle:
    1. Cleans up the target directory for the current cycle.
    2. Calls the Go export script with specific start and end times.
    3. Finds the exported CSV and moves it to the cycle-specific directory.
    4. Splits the full dataset into IS and OOS files based on a precise timestamp.

    Args:
        is_start_time: The start time for the IS data (format 'YYYY-MM-DD HH:MM:SS').
        is_end_time: The end time for the IS data, which is also the split point.
        oos_end_time: The end time for the OOS data.
        cycle_dir: The directory to store all artifacts for this cycle.

    Returns:
        A tuple containing the paths to the IS and OOS CSV files, respectively.
        Returns (None, None) if any step fails.
    """
    logging.info(f"WFO Cycle: Exporting data from {is_start_time} to {oos_end_time}")
    _cleanup_directory(cycle_dir)
    # Also clean the shared simulation export dir to easily find the new file
    _cleanup_directory(config.SIMULATION_DIR)

    cmd = [
        'go', 'run', 'cmd/export/main.go',
        f'--start={is_start_time}',
        f'--end={oos_end_time}',
        '--no-zip'
    ]

    try:
        process_env = os.environ.copy()
        subprocess.run(cmd, check=True, capture_output=True, text=True, cwd=config.APP_ROOT, env=process_env)

        # The export script writes to the default 'simulation' dir. Find it there.
        exported_csv = _find_latest_csv(config.SIMULATION_DIR)
        if not exported_csv:
            logging.error("No CSV file found after export.")
            return None, None

        # Move the exported file to the cycle-specific directory
        full_dataset_path = cycle_dir / "full_data.csv"
        shutil.move(exported_csv, full_dataset_path)

        logging.info(f"Successfully exported data to {full_dataset_path}")

        # Split the data within the cycle directory
        is_path, oos_path = _split_data_by_timestamp(
            full_dataset_path=full_dataset_path,
            split_time_str=is_end_time,
            cycle_dir=cycle_dir
        )
        return is_path, oos_path

    except subprocess.CalledProcessError as e:
        logging.error(f"Failed to export data: {e.stderr}")
        return None, None
    except FileNotFoundError:
        logging.error(f"Could not find 'go' executable. Ensure Go is installed and in the system's PATH.")
        return None, None


def _cleanup_directory(directory: Path):
    """Removes and recreates a directory to ensure a clean state."""
    if directory.exists():
        shutil.rmtree(directory)
    directory.mkdir(parents=True, exist_ok=True)


def _find_latest_csv(directory: Path) -> Union[Path, None]:
    """Finds the most recently modified CSV file in a directory."""
    if not directory.exists():
        return None
    exported_files = list(directory.glob("*.csv"))
    if not exported_files:
        return None
    exported_files.sort(key=os.path.getmtime, reverse=True)
    return exported_files[0]


def _split_data_by_timestamp(full_dataset_path: Path, split_time_str: str, cycle_dir: Path) -> tuple[Path, Path]:
    """
    Splits a CSV file into In-Sample (IS) and Out-of-Sample (OOS) sets based on a specific timestamp.
    """
    is_path = cycle_dir / "is_data.csv"
    oos_path = cycle_dir / "oos_data.csv"

    try:
        split_time = _parse_timestamp(split_time_str)
    except ValueError as e:
        logging.error(f"Invalid split_time_str format: {split_time_str}. Error: {e}")
        return is_path, oos_path

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

    with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
        f_is.write(header)
        f_oos.write(header)
        split_found = False
        for line in data_lines:
            try:
                current_time_str = line.split(',')[0]
                current_time = _parse_timestamp(current_time_str)
                if current_time < split_time:
                    f_is.write(line)
                else:
                    f_oos.write(line)
                    split_found = True
            except (ValueError, IndexError) as e:
                logging.warning(f"Skipping line due to parse error: {e}. Line: '{line.strip()}'")
                continue

        if not split_found:
            logging.warning(f"Split time {split_time_str} was after all data points. OOS will be empty.")

    is_lines = len((is_path).read_text().splitlines())
    oos_lines = len((oos_path).read_text().splitlines())
    logging.info(f"Split data into IS ({is_path}, {is_lines} lines) and OOS ({oos_path}, {oos_lines} lines)")

    return is_path, oos_path


def _parse_timestamp(ts_str: str) -> datetime:
    """
    Parses a timestamp string into a timezone-aware datetime object.
    Supports multiple common formats.
    """
    original_ts = ts_str.strip()

    # Handle ISO 8601 format, which is the most common
    try:
        # Replace space with T for full ISO 8601 compatibility
        if ' ' in original_ts and 'T' not in original_ts:
            original_ts = original_ts.replace(' ', 'T', 1)
        # Handle timezone info like Z, +00:00, -07:00
        if original_ts.endswith('Z'):
             original_ts = original_ts[:-1] + '+00:00'

        parsed_dt = datetime.fromisoformat(original_ts)
        if parsed_dt.tzinfo is None:
            # If no timezone is specified, assume UTC as per standard practice
            return parsed_dt.replace(tzinfo=timezone.utc)
        return parsed_dt
    except ValueError:
        # Fallback to strptime for other formats if fromisoformat fails
        pass

    formats_to_try = [
        '%Y-%m-%d %H:%M:%S',
        '%Y-%m-%d %H:%M:%S.%f',
    ]
    for fmt in formats_to_try:
        try:
            parsed = datetime.strptime(original_ts, fmt)
            # Assume UTC if naive
            return parsed.replace(tzinfo=timezone.utc)
        except ValueError:
            continue

    raise ValueError(f"Could not parse timestamp: '{ts_str}' with any known format.")


# --- Functions for the legacy daemon mode ---

def export_and_split_data_for_daemon(total_hours: float, oos_hours: float) -> Tuple[Union[Path, None], Union[Path, None]]:
    """
    Exports data from the database and splits it into In-Sample (IS) and Out-of-Sample (OOS).
    This function is used by the legacy daemon optimizer.
    """
    logging.info(f"Daemon mode: Exporting data for the last {total_hours} hours...")

    # The daemon can use the main simulation directory directly
    daemon_dir = config.SIMULATION_DIR
    _cleanup_directory(daemon_dir)

    import math
    cmd = [
        'go', 'run', 'cmd/export/main.go',
        f'--hours-before={math.ceil(total_hours)}',
        '--no-zip'
    ]

    try:
        process_env = os.environ.copy()
        subprocess.run(cmd, check=True, capture_output=True, text=True, cwd=config.APP_ROOT, env=process_env)

        full_dataset_path = _find_latest_csv(daemon_dir)
        if not full_dataset_path:
            logging.error("No CSV file found after export.")
            return None, None

        logging.info(f"Successfully exported data to {full_dataset_path}")

        is_path, oos_path = _split_data_by_ratio(
            full_dataset_path=full_dataset_path,
            total_hours=total_hours,
            oos_hours=oos_hours,
            output_dir=daemon_dir,
        )
        return is_path, oos_path

    except subprocess.CalledProcessError as e:
        logging.error(f"Failed to export data for daemon: {e.stderr}")
        return None, None

def _split_data_by_ratio(full_dataset_path: Path, total_hours: float, oos_hours: float, output_dir: Path) -> tuple[Path, Path]:
    """
    Splits a CSV file into In-Sample (IS) and Out-of-Sample (OOS) sets.
    The OOS set is defined as the last `oos_hours` of the data.
    The `total_hours` parameter is ignored, kept for compatibility.
    """
    is_path = output_dir / "is_data.csv"
    oos_path = output_dir / "oos_data.csv"

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
        # Determine the split time based on the timestamp of the last data line
        last_line_ts_str = data_lines[-1].split(',')[0]
        last_ts = _parse_timestamp(last_line_ts_str)
        split_time = last_ts - timedelta(hours=oos_hours)
        logging.info(f"Calculated split time for daemon mode: {split_time.isoformat()}")

    except (ValueError, IndexError) as e:
        logging.error(f"Could not determine split time from data due to error: {e}. Aborting split.")
        # Create empty files to prevent downstream errors
        with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
            f_is.write(header)
            f_oos.write(header)
        return is_path, oos_path

    # Split data based on the calculated timestamp
    with open(is_path, 'w') as f_is, open(oos_path, 'w') as f_oos:
        f_is.write(header)
        f_oos.write(header)
        split_found = False
        for line in data_lines:
            try:
                current_time_str = line.split(',')[0]
                current_time = _parse_timestamp(current_time_str)
                if current_time < split_time:
                    f_is.write(line)
                else:
                    f_oos.write(line)
                    split_found = True
            except (ValueError, IndexError) as e:
                logging.warning(f"Skipping line due to parse error: {e}. Line: '{line.strip()}'")
                continue

        if not split_found:
            logging.warning(f"Split time {split_time.isoformat()} was after all data points. OOS will be empty.")

    is_lines = len((is_path).read_text().splitlines())
    oos_lines = len((oos_path).read_text().splitlines())
    logging.info(f"Split data for daemon into IS ({is_path}, {is_lines} lines) and OOS ({oos_path}, {oos_lines} lines)")

    return is_path, oos_path
