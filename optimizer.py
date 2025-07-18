import yaml
import optuna
import subprocess
import os
import re
import json
import zipfile
import tempfile
import logging
import time
from jinja2 import Template
import sqlalchemy.exc
import sqlite3

# --- ロギング設定 ---
optuna_logger = logging.getLogger("optuna")
optuna_logger.setLevel(logging.WARNING)

# --- ZIP展開処理を関数の外に移動 ---
csv_path = os.getenv('CSV_PATH')
if not csv_path:
    raise ValueError("CSV_PATH environment variable is not set.")

unzipped_csv_path = csv_path
if csv_path.endswith('.zip'):
    print(f"Unzipping {csv_path}...")
    simulation_dir = 'simulation'
    os.makedirs(simulation_dir, exist_ok=True)
    with zipfile.ZipFile(csv_path, 'r') as zip_ref:
        zip_ref.extractall(simulation_dir)
    # 展開されたCSVファイル名を取得（ZIPファイル名から.zipを除いたものと仮定）
    unzipped_csv_path = os.path.join(simulation_dir, os.path.basename(csv_path)[:-4] + '.csv')
    print(f"Using unzipped CSV: {unzipped_csv_path}")


def objective(trial):
    # 1. パラメータ空間の定義
    params = {
        'spread_limit': trial.suggest_int('spread_limit', 10, 150),
        'lot_max_ratio': trial.suggest_float('lot_max_ratio', 0.01, 0.2),
        'order_ratio': trial.suggest_float('order_ratio', 0.05, 0.25),
        'adaptive_position_sizing_enabled': trial.suggest_categorical('adaptive_position_sizing_enabled', [True, False]),
        'adaptive_num_trades': trial.suggest_int('adaptive_num_trades', 3, 20),
        'adaptive_reduction_step': trial.suggest_float('adaptive_reduction_step', 0.5, 1.0),
        'adaptive_min_ratio': trial.suggest_float('adaptive_min_ratio', 0.1, 0.8),
        'long_obi_threshold': trial.suggest_float('long_obi_threshold', 0.1, 2.0),
        'long_tp': trial.suggest_int('long_tp', 50, 500),
        'long_sl': trial.suggest_int('long_sl', -500, -50),
        'short_obi_threshold': trial.suggest_float('short_obi_threshold', -2.0, -0.1),
        'short_tp': trial.suggest_int('short_tp', 50, 500),
        'short_sl': trial.suggest_int('short_sl', -500, -50),
        'hold_duration_ms': trial.suggest_int('hold_duration_ms', 100, 2000),
        'slope_filter_enabled': trial.suggest_categorical('slope_filter_enabled', [True, False]),
        'slope_period': trial.suggest_int('slope_period', 3, 50),
        'slope_threshold': trial.suggest_float('slope_threshold', 0.0, 0.5),
        'ewma_lambda': trial.suggest_float('ewma_lambda', 0.05, 0.3),
        'dynamic_obi_enabled': trial.suggest_categorical('dynamic_obi_enabled', [True, False]),
        'volatility_factor': trial.suggest_float('volatility_factor', 0.5, 5.0),
        'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.5, 1.0),
        'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.0, 3.0),
        'twap_enabled': trial.suggest_categorical('twap_enabled', [True, False]),
        'twap_max_order_size_btc': trial.suggest_float('twap_max_order_size_btc', 0.01, 0.1),
        'twap_interval_seconds': trial.suggest_int('twap_interval_seconds', 1, 10),
        'twap_partial_exit_enabled': trial.suggest_categorical('twap_partial_exit_enabled', [True, False]),
        'twap_profit_threshold': trial.suggest_float('twap_profit_threshold', 0.1, 2.0),
        'twap_exit_ratio': trial.suggest_float('twap_exit_ratio', 0.1, 1.0),
        'risk_max_drawdown_percent': trial.suggest_int('risk_max_drawdown_percent', 5, 20),
        'risk_max_position_ratio': trial.suggest_float('risk_max_position_ratio', 0.1, 1.0),
    }

    # 2. テンプレートから設定ファイルを生成
    with open('config/trade_config.yaml.template', 'r') as f:
        template_str = f.read()
    template = Template(template_str)
    rendered_config_str = template.render(params)
    trade_config = yaml.safe_load(rendered_config_str)


    # 更新した設定を一時ファイルに保存 (Goバイナリに渡すため)
    with tempfile.NamedTemporaryFile(mode='w', delete=False, suffix='.yaml') as temp_config_file:
        yaml.dump(trade_config, temp_config_file)
        temp_config_path = temp_config_file.name

    # 3. シミュレーションの実行 (Goバイナリを直接呼び出す)
    command = [
        './build/obi-scalp-bot',
        '--simulate',
        f'--csv={unzipped_csv_path}',
        f'--trade-config={temp_config_path}',
        '--json-output'
    ]

    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        try:
            summary = json.loads(result.stdout)
            total_profit = summary.get('TotalProfit', 0.0)
            risk_reward_ratio = summary.get('RiskRewardRatio', 0.0)
            return_vs_buy_and_hold = summary.get('ReturnVsBuyAndHold', 0.0)

            # 制約条件のチェック
            if total_profit < 0 or risk_reward_ratio < 1 or return_vs_buy_and_hold < 0:
                return 0.0

            # スコア計算
            # 各指標を正規化して合計する（スケールは仮）
            profit_score = min(total_profit / 10000.0, 1.0)
            risk_reward_score = min((risk_reward_ratio - 1.0) / (10.0 - 1.0), 1.0)
            return_vs_buy_and_hold_score = min(return_vs_buy_and_hold / 10000.0, 1.0)

            score = profit_score + risk_reward_score + return_vs_buy_and_hold_score
            return score

        except json.JSONDecodeError:
            print(f"Failed to parse JSON from simulation output: {result.stdout}")
            return 0.0
    except subprocess.CalledProcessError as e:
        print("Simulation failed.")
        print(f"--- STDOUT ---\n{e.stdout}")
        print(f"--- STDERR ---\n{e.stderr}")
        return 0.0
    finally:
        # 5. 一時ファイルのクリーンアップ
        if os.path.exists(temp_config_path):
            os.remove(temp_config_path)

if __name__ == '__main__':
    n_trials = int(os.getenv('N_TRIALS', '100'))
    study_name = os.getenv('STUDY_NAME', 'obi-scalp-bot-optimization')
    storage_url = os.getenv('STORAGE_URL', 'sqlite:///optuna_study.db')

    # catch 引数で OperationalError と StorageInternalError をリトライ
    catch_exceptions = (
        sqlalchemy.exc.OperationalError,
        optuna.exceptions.StorageInternalError,
        sqlite3.OperationalError,
    )

    study = optuna.create_study(
        study_name=study_name,
        storage=storage_url,
        load_if_exists=False,
        direction='maximize'
    )

    study.optimize(
        objective,
        n_trials=n_trials,
        n_jobs=-1,
        show_progress_bar=True,
        catch=catch_exceptions,
    )

    print("Best trial:")
    trial = study.best_trial
    print(f"  Value: {trial.value}")
    print("  Params: ")
    for key, value in trial.params.items():
        print(f"    {key}: {value}")

    # 最適な設定をテンプレートからYAMLファイルとして出力
    best_params = trial.params
    with open('config/trade_config.yaml.template', 'r') as f:
        template_str = f.read()
    template = Template(template_str)
    best_config_str = template.render(best_params)

    with open('config/best_trade_config.yaml', 'w') as f:
        f.write(best_config_str)

    print("\nBest trade config saved to config/best_trade_config.yaml")
