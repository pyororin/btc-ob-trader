import os
import time
import psycopg2
import psycopg2.extras
import logging
import json
from typing import Dict, Any, Optional, List

# Import the centralized config module
from . import config

# --- Configuration ---
METRICS_WINDOWS_HOURS = [4, 1]  # Check performance over the last 4 hours and 1 hour
BASELINE_DAYS = 7  # Use last 7 days for baseline statistics

# --- Logging Setup ---
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# Type alias for database connection
DbConnection = Any

def get_db_connection() -> Optional[DbConnection]:
    """
    Establishes and returns a connection to the TimescaleDB database.

    Uses credentials and connection details from the central config module.

    Returns:
        A psycopg2 connection object if successful, otherwise None.
    """
    try:
        conn = psycopg2.connect(
            dbname=config.DB_NAME,
            user=config.DB_USER,
            password=config.DB_PASSWORD,
            host=config.DB_HOST,
            port=config.DB_PORT
        )
        logging.info("Successfully connected to the database.")
        return conn
    except psycopg2.OperationalError as e:
        logging.error(f"Could not connect to the database: {e}")
        return None


def get_performance_metrics(conn: DbConnection, hours: float) -> Optional[Dict[str, float]]:
    """
    Fetches aggregated performance metrics from the pnl_reports table
    over a specified time window.

    It calculates the average Sharpe Ratio, average Profit Factor, and the
    maximum Max Drawdown over the period.

    Args:
        conn: The database connection object.
        hours: The time window in hours to look back for reports.

    Returns:
        A dictionary with aggregated performance metrics, or None if the query
        fails or if there is no data to aggregate.
    """
    minutes = int(hours * 60)
    query = """
        SELECT
            AVG(sharpe_ratio) AS sharpe_ratio,
            AVG(profit_factor) AS profit_factor,
            MAX(max_drawdown) AS max_drawdown
        FROM pnl_reports
        WHERE time >= NOW() - INTERVAL '1 minute' * %s;
    """
    try:
        with conn.cursor(cursor_factory=psycopg2.extras.DictCursor) as cur:
            cur.execute(query, (minutes,))
            result = cur.fetchone()
            # If there's no data in the window, AVG/MAX return NULL.
            # We should treat this as "no metrics found".
            if result and result['sharpe_ratio'] is not None:
                logging.info(
                    f"Aggregated metrics for last {hours}h: "
                    f"Sharpe={result['sharpe_ratio']:.2f}, "
                    f"PF={result['profit_factor']:.2f}, "
                    f"MDD={result['max_drawdown']:.2f}"
                )
                return dict(result)
    except psycopg2.Error as e:
        logging.error(f"Database error in get_performance_metrics: {e}")
        conn.rollback()

    logging.warning(f"Could not get aggregated metrics for last {hours}h. Returning None.")
    return None


def get_baseline_statistics(conn: DbConnection, days: int) -> Optional[Dict[str, float]]:
    """
    Calculates the moving average (mu) and standard deviation (sigma) of the
    Sharpe ratio over a specified number of days to use as a performance baseline.

    Args:
        conn: The database connection object.
        days: The number of days to look back for calculating the baseline.

    Returns:
        A dictionary with the mean and std dev of the Sharpe ratio, or None
        if the query fails or data is insufficient.
    """
    query = """
        SELECT AVG(sharpe_ratio) AS sharpe_mu, STDDEV(sharpe_ratio) AS sharpe_sigma
        FROM pnl_reports
        WHERE time >= NOW() - INTERVAL '1 day' * %s;
    """
    try:
        with conn.cursor(cursor_factory=psycopg2.extras.DictCursor) as cur:
            cur.execute(query, (days,))
            result = cur.fetchone()
            if result and result['sharpe_mu'] is not None:
                mu = result['sharpe_mu']
                # Ensure sigma is a non-zero float
                sigma = result['sharpe_sigma'] if result['sharpe_sigma'] is not None and result['sharpe_sigma'] > 0 else 0.1
                logging.info(f"Baseline stats ({days}d): Sharpe Mu={mu:.2f}, Sigma={sigma:.2f}")
                return {"sharpe_mu": mu, "sharpe_sigma": sigma}
    except psycopg2.Error as e:
        logging.error(f"Database error in get_baseline_statistics: {e}")
        conn.rollback()

    logging.warning("Could not retrieve baseline statistics. Returning None.")
    return None


def trigger_optimization(drift_details: Dict[str, Any]):
    """
    Creates a job file to signal the optimizer process to start.

    This monitoring script does not run the optimization directly. Instead, it
    communicates with the optimizer by creating a JSON file that the optimizer
    is watching for. The job no longer contains specific window sizes, as the
    new WFA process manages its own time windows based on its configuration.

    Args:
        drift_details: A dictionary containing the trigger type and severity.
    """
    job = {
        "trigger_type": drift_details["trigger_type"],
        "severity": drift_details["severity"],
        "timestamp": time.time()
    }
    try:
        config.PARAMS_DIR.mkdir(parents=True, exist_ok=True)
        with open(config.JOB_FILE, 'w') as f:
            json.dump(job, f)
        logging.critical(f"Optimization triggered by creating job file: {job}")
    except IOError as e:
        logging.error(f"Failed to create optimization job file: {e}")


def check_for_drift(metrics: Dict[float, Dict], baseline: Optional[Dict]) -> List[Dict]:
    """
    Checks for performance drift by comparing current metrics against the baseline.

    Args:
        metrics: A dictionary where keys are window hours and values are metric dicts.
        baseline: The baseline statistics (mu and sigma).

    Returns:
        A list of dictionaries, where each dictionary represents a detected
        drift event. Returns an empty list if no drift is detected.
    """
    if not baseline or not all(metrics.values()):
        logging.critical(
            "EMERGENCY TRIGGER (Incomplete Data): One or more metrics could not be retrieved."
        )
        return [{"trigger_type": "zero_metrics_fallback", "severity": "major"}]

    detected_drifts = []
    sharpe_mu, sharpe_sigma = baseline["sharpe_mu"], baseline["sharpe_sigma"]

    # Assumes METRICS_WINDOWS_HOURS is sorted from largest to smallest
    long_window, short_window = METRICS_WINDOWS_HOURS
    metrics_long = metrics[long_window]
    metrics_short = metrics[short_window]

    # --- Condition 1: Short-term Sharpe Ratio Drift (Minor) ---
    z_score_short = (metrics_short["sharpe_ratio"] - sharpe_mu) / sharpe_sigma
    if z_score_short < config.SHARPE_DRIFT_THRESHOLD_SD:
        logging.warning(
            f"DRIFT DETECTED (Short-term Sharpe, {short_window}h): Z-score={z_score_short:.2f} < {config.SHARPE_DRIFT_THRESHOLD_SD}"
        )
        detected_drifts.append({"trigger_type": "sharpe_drift_short_term", "severity": "minor"})

    # --- Condition 2: Emergency Sharpe Ratio Drop (Major) ---
    z_score_long = (metrics_long["sharpe_ratio"] - sharpe_mu) / sharpe_sigma
    if z_score_long < config.SHARPE_EMERGENCY_THRESHOLD_SD and z_score_short < config.SHARPE_EMERGENCY_THRESHOLD_SD:
        logging.critical(
            f"EMERGENCY TRIGGER (Sharpe Drop): {long_window}h Z={z_score_long:.2f}, {short_window}h Z={z_score_short:.2f}"
        )
        detected_drifts.append({"trigger_type": "sharpe_emergency_drop", "severity": "major"})

    # --- Condition 3: Profit Factor Degradation (Normal) ---
    # Use the longer window for PF check for more stability
    if 0 < metrics_long["profit_factor"] < config.PF_DRIFT_THRESHOLD:
        logging.warning(
            f"DRIFT DETECTED (Profit Factor, {long_window}h): PF={metrics_long['profit_factor']:.2f} < {config.PF_DRIFT_THRESHOLD}"
        )
        detected_drifts.append({"trigger_type": "profit_factor_drift", "severity": "normal"})

    # --- Condition 4: Zero Metrics Fallback (Major) ---
    if metrics_long["profit_factor"] == 0:
        logging.critical(
            "EMERGENCY TRIGGER (Zero Metrics): Profit factor is 0, indicating a potential data feed issue."
        )
        detected_drifts.append({"trigger_type": "zero_metrics_fallback", "severity": "major"})

    return detected_drifts


def main():
    """
    The main loop of the drift monitor.

    It periodically connects to the database, fetches performance metrics,
    checks for drift, and triggers re-optimization if necessary.
    """
    logging.info("Drift monitor started.")
    # Ensure windows are sorted from longest to shortest for logic in check_for_drift
    METRICS_WINDOWS_HOURS.sort(reverse=True)

    time.sleep(10)  # Startup delay

    conn = None
    try:
        conn = get_db_connection()
        if not conn:
            logging.error("Failed to get DB connection. Exiting.")
            return

        while True:
            logging.info("--- Running Drift Check ---")

            # 1. Get current performance for all defined windows
            all_metrics = {
                window: get_performance_metrics(conn, window)
                for window in METRICS_WINDOWS_HOURS
            }

            # 2. Get historical baseline
            baseline_stats = get_baseline_statistics(conn, BASELINE_DAYS)

            # 3. Check for drift conditions
            detected_drifts = check_for_drift(all_metrics, baseline_stats)

            # 4. If drift is detected, trigger optimization with the highest priority
            if detected_drifts:
                severity_order = {"major": 0, "normal": 1, "minor": 2}
                best_drift = min(detected_drifts, key=lambda x: severity_order[x['severity']])
                trigger_optimization(best_drift)

            logging.info(f"Drift check complete. Waiting for {config.CHECK_INTERVAL_SECONDS} seconds.")
            time.sleep(config.CHECK_INTERVAL_SECONDS)

    except KeyboardInterrupt:
        logging.info("Drift monitor stopped by user.")
    except Exception as e:
        logging.error(f"An unexpected error occurred in the main loop: {e}", exc_info=True)
    finally:
        if conn:
            conn.close()
            logging.info("Database connection closed.")


if __name__ == "__main__":
    main()
