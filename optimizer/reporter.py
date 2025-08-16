import pandas as pd
import numpy as np
import logging
from collections import deque
from datetime import datetime, timezone
import psycopg2
import psycopg2.extras
from typing import Dict, Any, Optional

# Import the centralized config module
from . import config

logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

def get_db_connection() -> Optional[psycopg2.extensions.connection]:
    """Establishes and returns a connection to the TimescaleDB database."""
    try:
        conn = psycopg2.connect(
            dbname=config.DB_NAME,
            user=config.DB_USER,
            password=config.DB_PASSWORD,
            host=config.DB_HOST,
            port=config.DB_PORT
        )
        return conn
    except psycopg2.OperationalError as e:
        logging.error(f"Could not connect to the database: {e}")
        return None

def calculate_metrics_from_trade_log(csv_path: str) -> dict:
    """
    Calculates a comprehensive set of performance metrics from a CSV log of trades.
    """
    try:
        df_raw = pd.read_csv(csv_path)
    except FileNotFoundError:
        logging.error(f"Trade log file not found at: {csv_path}")
        return {}

    # --- Data Cleaning and Preparation ---
    df_raw['time'] = pd.to_datetime(df_raw['time'])
    df_raw['price'] = pd.to_numeric(df_raw['price'], errors='coerce')
    df_raw['size'] = pd.to_numeric(df_raw['size'], errors='coerce')
    df_raw.dropna(subset=['price', 'size'], inplace=True)

    cancelled_trades = df_raw['is_cancelled'].sum()
    df = df_raw[df_raw['is_cancelled'] != 1].copy()
    df = df.sort_values('time').reset_index(drop=True)

    if df.empty:
        logging.warning("Trade log is empty after cleaning. No metrics to calculate.")
        return {}

    buys = deque()
    pnl_history = []
    gross_profit = 0
    gross_loss = 0
    winning_trades = 0
    losing_trades = 0

    # --- PnL Calculation (FIFO) ---
    for _, trade in df.iterrows():
        if trade['side'] == 'buy':
            buys.append(trade)
        elif trade['side'] == 'sell':
            if not buys:
                continue
            buy_trade = buys.popleft()
            pnl = (trade['price'] - buy_trade['price']) * trade['size']
            pnl_history.append(pnl)
            if pnl > 0:
                gross_profit += pnl
                winning_trades += 1
            else:
                gross_loss += pnl
                losing_trades += 1

    total_trades = len(pnl_history)
    if total_trades == 0:
        return {} # Return empty if no round-trip trades

    # --- Metrics Calculation ---
    pnl_array = np.array(pnl_history)
    profit_factor = gross_profit / abs(gross_loss) if gross_loss != 0 else np.inf

    trade_duration_seconds = (df['time'].iloc[-1] - df['time'].iloc[0]).total_seconds()
    # Avoid division by zero; assume at least 1 day if duration is short
    trade_duration_days = max(1, trade_duration_seconds / (24 * 3600))
    trades_per_day = total_trades / trade_duration_days

    annualization_factor = np.sqrt(252 * trades_per_day) if trades_per_day > 0 else np.sqrt(252)
    sharpe_ratio = np.mean(pnl_array) / np.std(pnl_array) * annualization_factor if np.std(pnl_array) > 0 else 0.0

    cumulative_pnl = np.cumsum(pnl_array)
    peak = np.maximum.accumulate(cumulative_pnl)
    max_drawdown = float(np.max(peak - cumulative_pnl))
    win_rate = winning_trades / total_trades if total_trades > 0 else 0.0

    # --- Build Full Summary for DB ---
    summary = {
        'time': datetime.now(timezone.utc),
        'start_date': df['time'].min().to_pydatetime(),
        'end_date': df['time'].max().to_pydatetime(),
        'total_trades': total_trades,
        'cancelled_trades': int(cancelled_trades),
        'cancellation_rate': cancelled_trades / (total_trades + cancelled_trades) if (total_trades + cancelled_trades) > 0 else 0.0,
        'winning_trades': winning_trades,
        'losing_trades': losing_trades,
        'win_rate': win_rate,
        'total_pnl': np.sum(pnl_array),
        'average_profit': gross_profit / winning_trades if winning_trades > 0 else 0.0,
        'average_loss': gross_loss / losing_trades if losing_trades > 0 else 0.0,
        'risk_reward_ratio': (gross_profit / winning_trades) / abs(gross_loss / losing_trades) if winning_trades > 0 and losing_trades > 0 else 0.0,
        'profit_factor': profit_factor,
        'sharpe_ratio': sharpe_ratio,
        'max_drawdown': max_drawdown,
        # Add required default values for other NOT NULL columns
        'long_winning_trades': 0, 'long_losing_trades': 0, 'long_win_rate': 0.0,
        'short_winning_trades': 0, 'short_losing_trades': 0, 'short_win_rate': 0.0,
        'sortino_ratio': 0.0, 'calmar_ratio': 0.0, 'recovery_factor': 0.0,
        'average_holding_period_seconds': 0.0, 'average_winning_holding_period_seconds': 0.0,
        'average_losing_holding_period_seconds': 0.0, 'max_consecutive_wins': 0,
        'max_consecutive_losses': 0, 'buy_and_hold_return': 0.0,
        'return_vs_buy_and_hold': 0.0, 'last_trade_id': df['transaction_id'].iloc[-1] if 'transaction_id' in df.columns else 0,
        'cumulative_total_pnl': np.sum(pnl_array), 'cumulative_winning_trades': winning_trades,
        'cumulative_losing_trades': losing_trades,
        # Keep PnlHistory for other potential uses (like in objective.py)
        'PnlHistory': pnl_history
    }
    logging.info(f"Calculated metrics from trade log.")
    return summary

def insert_report_to_db(summary: Dict[str, Any]):
    """
    Inserts a calculated performance report into the pnl_reports table.
    """
    if not summary:
        logging.warning("Summary is empty, skipping database insertion.")
        return

    conn = get_db_connection()
    if not conn:
        logging.error("Cannot insert report: failed to connect to database.")
        return

    # Map summary keys to DB columns and handle potential missing keys
    db_record = {k: summary.get(k) for k in [
        'time', 'start_date', 'end_date', 'total_trades', 'cancelled_trades',
        'cancellation_rate', 'winning_trades', 'losing_trades', 'win_rate',
        'long_winning_trades', 'long_losing_trades', 'long_win_rate',
        'short_winning_trades', 'short_losing_trades', 'short_win_rate',
        'total_pnl', 'average_profit', 'average_loss', 'risk_reward_ratio',
        'profit_factor', 'sharpe_ratio', 'sortino_ratio', 'calmar_ratio',
        'max_drawdown', 'recovery_factor', 'average_holding_period_seconds',
        'average_winning_holding_period_seconds', 'average_losing_holding_period_seconds',
        'max_consecutive_wins', 'max_consecutive_losses', 'buy_and_hold_return',
        'return_vs_buy_and_hold', 'last_trade_id', 'cumulative_total_pnl',
        'cumulative_winning_trades', 'cumulative_losing_trades'
    ]}

    # Ensure no None values are passed for NOT NULL columns
    for key, value in db_record.items():
        if value is None:
            logging.error(f"Cannot insert report: summary is missing required key '{key}'.")
            return

    query = """
        INSERT INTO pnl_reports (
            time, start_date, end_date, total_trades, cancelled_trades,
            cancellation_rate, winning_trades, losing_trades, win_rate,
            long_winning_trades, long_losing_trades, long_win_rate,
            short_winning_trades, short_losing_trades, short_win_rate,
            total_pnl, average_profit, average_loss, risk_reward_ratio,
            profit_factor, sharpe_ratio, sortino_ratio, calmar_ratio,
            max_drawdown, recovery_factor, average_holding_period_seconds,
            average_winning_holding_period_seconds, average_losing_holding_period_seconds,
            max_consecutive_wins, max_consecutive_losses, buy_and_hold_return,
            return_vs_buy_and_hold, last_trade_id, cumulative_total_pnl,
            cumulative_winning_trades, cumulative_losing_trades
        ) VALUES (
            %(time)s, %(start_date)s, %(end_date)s, %(total_trades)s, %(cancelled_trades)s,
            %(cancellation_rate)s, %(winning_trades)s, %(losing_trades)s, %(win_rate)s,
            %(long_winning_trades)s, %(long_losing_trades)s, %(long_win_rate)s,
            %(short_winning_trades)s, %(short_losing_trades)s, %(short_win_rate)s,
            %(total_pnl)s, %(average_profit)s, %(average_loss)s, %(risk_reward_ratio)s,
            %(profit_factor)s, %(sharpe_ratio)s, %(sortino_ratio)s, %(calmar_ratio)s,
            %(max_drawdown)s, %(recovery_factor)s, %(average_holding_period_seconds)s,
            %(average_winning_holding_period_seconds)s, %(average_losing_holding_period_seconds)s,
            %(max_consecutive_wins)s, %(max_consecutive_losses)s, %(buy_and_hold_return)s,
            %(return_vs_buy_and_hold)s, %(last_trade_id)s, %(cumulative_total_pnl)s,
            %(cumulative_winning_trades)s, %(cumulative_losing_trades)s
        )
    """

    try:
        with conn.cursor() as cur:
            cur.execute(query, db_record)
        conn.commit()
        logging.info("Successfully inserted performance report into the database.")
    except psycopg2.Error as e:
        logging.error(f"Database error during report insertion: {e}")
        conn.rollback()
    finally:
        if conn:
            conn.close()
