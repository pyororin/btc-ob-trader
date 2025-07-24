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
# --- 環境変数 ---
# データベース接続情報
DB_USER = os.getenv('DB_USER')  # データベースのユーザー名
DB_PASSWORD = os.getenv('DB_PASSWORD')  # データベースのパスワード
DB_NAME = os.getenv('DB_NAME')  # データベース名
DB_HOST = os.getenv('DB_HOST', 'timescaledb')  # データベースのホスト名 (デフォルト: timescaledb)
DB_PORT = os.getenv('DB_PORT', '5432')  # データベースのポート番号 (デフォルト: 5432)

# スクリプトの動作設定
CHECK_INTERVAL_SECONDS = int(
    os.getenv('CHECK_INTERVAL_SECONDS', '300')
)  # パフォーマンスチェックを実行する間隔（秒単位, デフォルト: 300秒 = 5分）
PARAMS_DIR = Path(os.getenv('PARAMS_DIR', '/data/params'))  # 最適化ジョブファイルを配置するディレクトリ


# --- Trigger Thresholds (can be moved to a config file) ---
# --- 最適化をトリガーする閾値 ---
# これらの値は、パフォーマンスの悪化を検知し、パラメータ最適化をいつ開始するかを決定するために使用されます。
# 設定ファイルに切り出すことも可能です。

# シャープレシオのZスコアがこの閾値を下回った場合に「軽微なドリフト」と判断します。
# Zスコアは、現在のシャープレシオが過去の平均からどれだけ標準偏差分離れているかを示す指標です。
# (現在値 - 平均) / 標準偏差 で計算されます。
SHARPE_DRIFT_THRESHOLD_SD = -0.5

# プロフィットファクター（総利益 / 総損失）がこの閾値を下回った場合に「ドリフト」と判断します。
# 1.0を下回ると損失が出ている状態を示します。
PF_DRIFT_THRESHOLD = 0.9

# シャープレシオのZスコアがこの閾値を下回った場合に「緊急事態」と判断し、
# より迅速な対応（短い学習期間での最適化）を行います。
SHARPE_EMERGENCY_THRESHOLD_SD = -1.0


def get_db_connection():
    """
    TimescaleDBデータベースへの接続を確立します。

    環境変数から読み取った接続情報（ユーザー、パスワード、ホストなど）を使用して、
    PostgreSQLデータベース（TimescaleDB）への接続を試みます。
    接続に成功した場合は接続オブジェクトを返し、失敗した場合はNoneを返します。

    Returns:
        psycopg2.connection or None: 接続成功時は接続オブジェクト、失敗時はNone。
    """
    try:
        conn = psycopg2.connect(
            dbname=DB_NAME,
            user=DB_USER,
            password=DB_PASSWORD,
            host=DB_HOST,
            port=DB_PORT
        )
        logging.info("Successfully connected to the database.")
        return conn
    except psycopg2.OperationalError as e:
        logging.error(f"Could not connect to the database: {e}")
        return None


def get_performance_metrics(conn, hours):
    """
    指定された時間枠のパフォーマンス指標（シャープレシオ、プロフィットファクター、最大ドローダウン）をデータベースから取得します。

    pnl_reportsテーブルから、現在時刻から指定された時間（hours）前までの最新のパフォーマンス指標を取得します。
    データベースクエリが失敗した場合や、該当期間のデータが存在しない場合には、
    フォールバックとしてハードコードされた模擬データを返します。

    Args:
        conn (psycopg2.connection): データベース接続オブジェクト。
        hours (float or int): パフォーマンス指標を取得する期間（時間単位）。

    Returns:
        dict: パフォーマンス指標（sharpe_ratio, profit_factor, max_drawdown）を含む辞書。
              データ取得に失敗した場合は、模擬データが返されます。
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
    過去7日間のシャープレシオの移動平均（mu）と標準偏差（sigma）を計算します。

    これらの統計量は、現在のパフォーマンスが過去のトレンドからどれだけ逸脱しているか（Zスコア）
    を判断するためのベースラインとして使用されます。
    データベースクエリが失敗した場合や、データが存在しない場合、あるいは標準偏差が0になるような
    場合には、フォールバックとしてハードコードされた模擬データを返します。

    Args:
        conn (psycopg2.connection): データベース接続オブジェクト。

    Returns:
        dict: シャープレシオの平均（sharpe_ratio_mu）と標準偏差（sharpe_ratio_sigma）を含む辞書。
              データ取得に失敗した場合は、模擬データが返されます。
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
    """
    別のプロセス（オプティマイザ）に最適化の実行を指示するジョブファイルを作成します。

    このモニタリングスクリプトは直接最適化を実行しません。代わりに、
    この関数を呼び出して、指定されたパラメータを含むJSONファイル（ジョブファイル）を
    `PARAMS_DIR`に作成します。
    別のオプティマイザプロセスがこのファイルを監視し、ファイルが作成されると
    最適化タスクを開始する仕組みです。

    Args:
        trigger_type (str): 最適化がトリガーされた理由を示す文字列（例: "sharpe_drift_short_term"）。
        window_is (float or int): 最適化のインサンプル期間（学習データ期間）の時間数。
        window_oos (float or int): 最適化のアウトオブサンプル期間（評価データ期間）の時間数。
    """
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
    logging.info(f"Optimization triggered by creating a job file: {job}")


def main():
    """
    ドリフトモニタのメインループ。

    無限ループ内で以下の処理を定期的に（CHECK_INTERVAL_SECONDSごとに）実行します。
    1. データベースへの接続を試みます。接続できない場合は終了します。
    2. 短期（15分）および中期（1時間）のパフォーマンス指標を取得します。
    3. 長期（7日間）のパフォーマンス統計（移動平均、標準偏差）を取得します。
    4. パフォーマンスのドリフト（悪化）を検知するための複数の条件をチェックします。
    5. ドリフトが検知された場合、問題の深刻度に応じたパラメータで最適化をトリガーします。
    6. 一定時間待機し、次のチェックサイクルへ移ります。
    """
    logging.info("Drift monitor started.")
    conn = None  # 接続オブジェクトを初期化
    try:
        # --- データベース接続 ---
        conn = get_db_connection()
        if not conn:
            logging.error("Failed to get DB connection. Exiting.")
            return

        # --- メインループ ---
        while True:
            logging.info("--- Running Drift Check ---")

            # 1. 現在のパフォーマンス指標を取得
            # 中期的なパフォーマンス（1時間）
            metrics_1h = get_performance_metrics(conn, 1)
            # 短期的なパフォーマンス（15分）
            metrics_15m = get_performance_metrics(conn, 0.25)

            # 2. 過去のパフォーマンス統計（ベースライン）を取得
            # 過去7日間の平均と標準偏差
            stats = get_moving_averages(conn)

            # 3. ドリフト（市場状況の変化によるパフォーマンス悪化）の条件をチェック
            # 標準偏差が0より大きいことを確認（ゼロ除算を避けるため）
            if stats and stats["sharpe_ratio_sigma"] > 0:
                # --- 条件1: 短期的なシャープレシオの悪化（軽微なドリフト）---
                # 直近15分のシャープレシオからZスコアを計算
                z_score = (
                    (metrics_15m["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                # Zスコアが軽微なドリフトの閾値を下回ったか？
                if z_score < SHARPE_DRIFT_THRESHOLD_SD:
                    log_msg = (
                        "DRIFT DETECTED (Short-term Sharpe): "
                        f"Z-score={z_score:.2f} < {SHARPE_DRIFT_THRESHOLD_SD}"
                    )
                    logging.warning(log_msg)
                    # → 軽微なドリフトと判断し、比較的短い期間で再最適化をトリガー
                    #    - IS (In-Sample): 2時間, OOS (Out-of-Sample): 30分
                    trigger_optimization(
                        "sharpe_drift_short_term",
                        window_is=2,
                        window_oos=0.5
                    )

                # --- 条件2: 緊急レベルのシャープレシオ低下（深刻なドリフト）---
                # 1時間と15分の両方のZスコアを計算
                z_1h = (
                    (metrics_1h["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                z_15m = (
                    (metrics_15m["sharpe_ratio"] - stats["sharpe_ratio_mu"]) /
                    stats["sharpe_ratio_sigma"]
                )
                # どちらかのZスコアが緊急閾値を下回ったか？
                if z_1h < SHARPE_EMERGENCY_THRESHOLD_SD or \
                   z_15m < SHARPE_EMERGENCY_THRESHOLD_SD:
                    log_msg = (
                        "EMERGENCY TRIGGER (Sharpe Drop): "
                        f"1h Z={z_1h:.2f}, 15m Z={z_15m:.2f}"
                    )
                    logging.critical(log_msg)
                    # → 市場の急変と判断し、非常に短い期間で迅速に再最適化をトリガー
                    #    - IS: 60分, OOS: 10分
                    trigger_optimization(
                        "sharpe_emergency_drop",
                        window_is=1,
                        window_oos=10/60
                    )

            # --- 条件3: プロフィットファクターの悪化（安定性の低下）---
            # 直近1時間のプロフィットファクターが閾値を下回ったか？
            if metrics_1h["profit_factor"] < PF_DRIFT_THRESHOLD:
                log_msg = (
                    "DRIFT DETECTED (Profit Factor): "
                    f"PF={metrics_1h['profit_factor']:.2f} < "
                    f"{PF_DRIFT_THRESHOLD}"
                )
                logging.warning(log_msg)
                # → 定期的なパラメータ更新と判断し、標準的な期間で再最適化をトリガー
                #    - IS: 4時間, OOS: 1時間
                trigger_optimization(
                    "profit_factor_drift",
                    window_is=4,
                    window_oos=1
                )

            # --- 次のチェックまで待機 ---
            logging.info(
                "Drift check complete. Waiting for "
                f"{CHECK_INTERVAL_SECONDS} seconds."
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
