import os
import time
import psycopg2
import psycopg2.extras
import logging
import json
from pathlib import Path

# --- Logging Setup ---
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)

# --- Environment Variables ---
DB_USER = os.getenv('DB_USER')
DB_PASSWORD = os.getenv('DB_PASSWORD')
DB_NAME = os.getenv('DB_NAME')
DB_HOST = os.getenv('DB_HOST', 'timescaledb')
DB_PORT = os.getenv('DB_PORT', '5432')
CHECK_INTERVAL_SECONDS = int(
    os.getenv('CHECK_INTERVAL_SECONDS', '300')
)  # 5 min
PARAMS_DIR = Path(os.getenv('PARAMS_DIR', '/data/params'))


# --- Trigger Thresholds (can be moved to a config file) ---
SHARPE_DRIFT_THRESHOLD_SD = -0.5
PF_DRIFT_THRESHOLD = 0.9
SHARPE_EMERGENCY_THRESHOLD_SD = -1.0


def get_db_connection():
    """Establishes a connection to the TimescaleDB."""
    try:
        conn = psycopg2.connect(
            dbname=DB_NAME,
            user=DB_USER,
            password=DB_PASSWORD,
            host=DB_HOST,
            port=DB_PORT
        )
        return conn
    except psycopg2.OperationalError as e:
        logging.error(f"Could not connect to database: {e}")
        return None


def get_performance_metrics(conn, hours):
    """
    Calculates Sharpe Ratio, Profit Factor, and Drawdown for a given time
    window.
    """
    logging.info(
        f"Calculating performance metrics for the last {hours} hours..."
    )
    query = """
        SELECT
            sharpe_ratio,
            profit_factor,
            max_drawdown
        FROM pnl_reports
        WHERE time >= NOW() - INTERVAL '%s hours'
        ORDER BY time DESC
        LIMIT 1;
    """
    try:
        with conn.cursor(cursor_factory=psycopg2.extras.DictCursor) as cur:
            cur.execute(query, (hours,))
            result = cur.fetchone()
            if result:
                log_msg = (
                    f"Metrics for last {hours}h: "
                    f"Sharpe={result['sharpe_ratio']:.2f}, "
                    f"PF={result['profit_factor']:.2f}, "
                    f"MDD={result['max_drawdown']:.2f}"
                )
                logging.info(log_msg)
                return {
                    "sharpe_ratio": result["sharpe_ratio"],
                    "profit_factor": result["profit_factor"],
                    "max_drawdown": result["max_drawdown"]
                }
    except psycopg2.Error as e:
        logging.error(f"Database error in get_performance_metrics: {e}")
        conn.rollback()  # Rollback on error

    # Fallback to simulated data if query fails or returns no data
    logging.warning(
        f"Could not get metrics for last {hours}h. Using mock data."
    )
    return {
        "sharpe_ratio": 0.8,
        "profit_factor": 1.1,
        "max_drawdown": 0.05
    }


def get_moving_averages(conn):
    """
    Calculates moving averages and standard deviations for metrics from the
    last 7 days.
    """
    logging.info("Calculating moving averages for the last 7 days...")
    query = """
        SELECT
            AVG(sharpe_ratio) AS sharpe_ratio_mu,
            STDDEV(sharpe_ratio) AS sharpe_ratio_sigma
        FROM pnl_reports
        WHERE time >= NOW() - INTERVAL '7 days';
    """
    try:
        with conn.cursor(cursor_factory=psycopg2.extras.DictCursor) as cur:
            cur.execute(query)
            result = cur.fetchone()
            # Handle case where result might be None or contain None values
            if result and result['sharpe_ratio_mu'] is not None and \
               result['sharpe_ratio_sigma'] is not None:
                log_msg = (
                    "Moving Averages: "
                    f"Sharpe Mu={result['sharpe_ratio_mu']:.2f}, "
                    f"Sigma={result['sharpe_ratio_sigma']:.2f}"
                )
                logging.info(log_msg)
                return {
                    "sharpe_ratio_mu": result["sharpe_ratio_mu"],
                    "sharpe_ratio_sigma": result["sharpe_ratio_sigma"]
                }
            # If sigma is 0 or null, return a small default value
            elif result and result['sharpe_ratio_mu'] is not None:
                logging.warning("Sharpe ratio sigma is zero/null. Using 0.1.")
                return {
                    "sharpe_ratio_mu": result["sharpe_ratio_mu"],
                    "sharpe_ratio_sigma": 0.1
                }

    except psycopg2.Error as e:
        logging.error(f"Database error in get_moving_averages: {e}")
        conn.rollback()

    # Fallback to simulated data if query fails or returns no data
    logging.warning("Could not retrieve moving averages. Using mock data.")
    return {
        "sharpe_ratio_mu": 1.0,
        "sharpe_ratio_sigma": 0.4
    }


def trigger_optimization(trigger_type, window_is, window_oos):
    """Triggers the optimizer by creating a job file."""
    job = {
        "trigger_type": trigger_type,
        "window_is_hours": window_is,
        "window_oos_hours": window_oos,
        "timestamp": time.time()
    }
    PARAMS_DIR.mkdir(parents=True, exist_ok=True)
    job_file = PARAMS_DIR / 'optimization_job.json'
    with open(job_file, 'w') as f:
        json.dump(job, f)
    logging.info(f"Optimization triggered: {job}")


def main():
    """Main loop for the drift monitor."""
    logging.info("Drift monitor started.")
    conn = None  # Initialize conn to None
    try:
        conn = get_db_connection()
        if not conn:
            logging.error("Failed to get DB connection. Exiting.")
            return

        while True:
            logging.info("--- Running Drift Check ---")

            # 1. Get current performance metrics
            metrics_1h = get_performance_metrics(conn, 1)
            metrics_15m = get_performance_metrics(conn, 0.25)

            # 2. Get historical performance statistics
            stats = get_moving_averages(conn)

            # 3. Check for drift conditions
            if stats and stats["sharpe_ratio_sigma"] > 0:  # Avoid div by zero
                # Cond 1: Short-term (15m) Sharpe Ratio Drift
                z_score = (
                    (metrics_15m["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                if z_score < SHARPE_DRIFT_THRESHOLD_SD:
                    log_msg = (
                        "DRIFT DETECTED (Short-term Sharpe): "
                        f"Z-score={z_score:.2f} < {SHARPE_DRIFT_THRESHOLD_SD}"
                    )
                    logging.warning(log_msg)
                    # Mild Drift: IS=2h, OOS=30m
                    trigger_optimization(
                        "sharpe_drift_short_term",
                        window_is=2,
                        window_oos=0.5
                    )

                # Cond 2: Emergency Sharpe Ratio Drop (1h or 15m)
                z_1h = (
                    (metrics_1h["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                z_15m = (
                    (metrics_15m["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                if z_1h < SHARPE_EMERGENCY_THRESHOLD_SD or \
                   z_15m < SHARPE_EMERGENCY_THRESHOLD_SD:
                    log_msg = (
                        "EMERGENCY TRIGGER (Sharpe Drop): "
                        f"1h Z={z_1h:.2f}, 15m Z={z_15m:.2f}"
                    )
                    logging.critical(log_msg)
                    # Shock: IS=60m, OOS=10m
                    trigger_optimization(
                        "sharpe_emergency_drop",
                        window_is=1,
                        window_oos=10/60
                    )

            # Cond 3: Profit Factor Drift (1h) -> Regular Refresh
            if metrics_1h["profit_factor"] < PF_DRIFT_THRESHOLD:
                log_msg = (
                    "DRIFT DETECTED (Profit Factor): "
                    f"PF={metrics_1h['profit_factor']:.2f} < "
                    f"{PF_DRIFT_THRESHOLD}"
                )
                logging.warning(log_msg)
                # Regular Refresh: IS=4h, OOS=1h
                trigger_optimization(
                    "profit_factor_drift",
                    window_is=4,
                    window_oos=1
                )

            logging.info(
                f"Drift check complete. Waiting for {CHECK_INTERVAL_SECONDS}s."
            )
            time.sleep(CHECK_INTERVAL_SECONDS)

    except KeyboardInterrupt:
        logging.info("Drift monitor stopped by user.")
    except Exception as e:
        logging.error(f"An unexpected error occurred: {e}", exc_info=True)
    finally:
        if conn:
            conn.close()
            logging.info("Database connection closed.")


if __name__ == "__main__":
    main()
