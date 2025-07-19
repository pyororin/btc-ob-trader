import yaml
import optuna
import subprocess
import os
import re
import json
import zipfile
import tempfile

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

    # 2. trade_config.yaml の読み込みと更新
    with open('config/trade_config.yaml', 'r') as f:
        trade_config = yaml.safe_load(f)

    # パラメータを trade_config に設定 (trial.paramsから取得するように修正)
    trade_config['spread_limit'] = trial.params['spread_limit']
    trade_config['lot_max_ratio'] = trial.params['lot_max_ratio']
    trade_config['order_ratio'] = trial.params['order_ratio']
    trade_config['adaptive_position_sizing']['enabled'] = trial.params['adaptive_position_sizing_enabled']
    trade_config['adaptive_position_sizing']['num_trades'] = trial.params['adaptive_num_trades']
    trade_config['adaptive_position_sizing']['reduction_step'] = trial.params['adaptive_reduction_step']
    trade_config['adaptive_position_sizing']['min_ratio'] = trial.params['adaptive_min_ratio']
    trade_config['long']['obi_threshold'] = trial.params['long_obi_threshold']
    trade_config['long']['tp'] = trial.params['long_tp']
    trade_config['long']['sl'] = trial.params['long_sl']
    trade_config['short']['obi_threshold'] = trial.params['short_obi_threshold']
    trade_config['short']['tp'] = trial.params['short_tp']
    trade_config['short']['sl'] = trial.params['short_sl']
    trade_config['signal']['hold_duration_ms'] = trial.params['hold_duration_ms']
    trade_config['signal']['slope_filter']['enabled'] = trial.params['slope_filter_enabled']
    trade_config['signal']['slope_filter']['period'] = trial.params['slope_period']
    trade_config['signal']['slope_filter']['threshold'] = trial.params['slope_threshold']
    trade_config['volatility']['ewma_lambda'] = trial.params['ewma_lambda']
    trade_config['volatility']['dynamic_obi']['enabled'] = trial.params['dynamic_obi_enabled']
    trade_config['volatility']['dynamic_obi']['volatility_factor'] = trial.params['volatility_factor']
    trade_config['volatility']['dynamic_obi']['min_threshold_factor'] = trial.params['min_threshold_factor']
    trade_config['volatility']['dynamic_obi']['max_threshold_factor'] = trial.params['max_threshold_factor']
    trade_config['twap']['enabled'] = trial.params['twap_enabled']
    trade_config['twap']['max_order_size_btc'] = trial.params['twap_max_order_size_btc']
    trade_config['twap']['interval_seconds'] = trial.params['twap_interval_seconds']
    trade_config['twap']['partial_exit_enabled'] = trial.params['twap_partial_exit_enabled']
    trade_config['twap']['profit_threshold'] = trial.params['twap_profit_threshold']
    trade_config['twap']['exit_ratio'] = trial.params['twap_exit_ratio']
    trade_config['risk']['max_drawdown_percent'] = trial.params['risk_max_drawdown_percent']
    trade_config['risk']['max_position_ratio'] = trial.params['risk_max_position_ratio']

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
                # 制約を満たさない場合は、非常に悪い値を返す
                return 0.0, 0.0, 0.0

        except json.JSONDecodeError:
            print(f"Failed to parse JSON from simulation output: {result.stdout}")
            return 0.0, 0.0, 0.0
    except subprocess.CalledProcessError as e:
        print("Simulation failed.")
        print(f"--- STDOUT ---\n{e.stdout}")
        print(f"--- STDERR ---\n{e.stderr}")
        # 失敗した場合は最小値を返す
        return 0.0, 0.0, 0.0
    finally:
        # 5. 一時ファイルのクリーンアップ
        if os.path.exists(temp_config_path):
            os.remove(temp_config_path)

    return total_profit, risk_reward_ratio, return_vs_buy_and_hold

if __name__ == '__main__':
    n_trials = int(os.getenv('N_TRIALS', '100'))
    study_name = os.getenv('STUDY_NAME', 'obi-scalp-bot-optimization')
    storage_url = os.getenv('STORAGE_URL', 'sqlite:///optuna_study.db')

    study = optuna.create_study(
        study_name=study_name,
        storage=storage_url,
        load_if_exists=True,
        directions=['maximize', 'maximize', 'maximize']
    )
    # n_jobs=-1 を指定して並列実行
    study.optimize(objective, n_trials=n_trials, n_jobs=-1)

    print("Best trials on the Pareto front:")
    best_trials = sorted(study.best_trials, key=lambda t: t.values[0], reverse=True)

    for trial in best_trials:
        print(f"  Trial {trial.number}:")
        print(f"    Values: TotalProfit={trial.values[0]:.2f}, RiskRewardRatio={trial.values[1]:.2f}, ReturnVsBuyAndHold={trial.values[2]:.2f}")
        print("    Params: ")
        for key, value in trial.params.items():
            print(f"      {key}: {value}")

    # TotalProfitが最も高いものを選択
    best_trial = best_trials[0]
    print("\nSelected best trial (highest TotalProfit):")
    print(f"  Trial {best_trial.number}:")
    print(f"    Values: TotalProfit={best_trial.values[0]:.2f}, RiskRewardRatio={best_trial.values[1]:.2f}, ReturnVsBuyAndHold={best_trial.values[2]:.2f}")


    # 最適な設定をYAMLファイルとして出力
    best_params = best_trial.params
    with open('config/trade_config.yaml', 'r') as f:
        best_config = yaml.safe_load(f)

    best_config['spread_limit'] = best_params.get('spread_limit')
    best_config['lot_max_ratio'] = best_params.get('lot_max_ratio')
    best_config['order_ratio'] = best_params.get('order_ratio')
    best_config['adaptive_position_sizing']['enabled'] = best_params.get('adaptive_position_sizing_enabled')
    best_config['adaptive_position_sizing']['num_trades'] = best_params.get('adaptive_num_trades')
    best_config['adaptive_position_sizing']['reduction_step'] = best_params.get('adaptive_reduction_step')
    best_config['adaptive_position_sizing']['min_ratio'] = best_params.get('adaptive_min_ratio')
    best_config['long']['obi_threshold'] = best_params.get('long_obi_threshold')
    best_config['long']['tp'] = best_params.get('long_tp')
    best_config['long']['sl'] = best_params.get('long_sl')
    best_config['short']['obi_threshold'] = best_params.get('short_obi_threshold')
    best_config['short']['tp'] = best_params.get('short_tp')
    best_config['short']['sl'] = best_params.get('short_sl')
    best_config['signal']['hold_duration_ms'] = best_params.get('hold_duration_ms')
    best_config['signal']['slope_filter']['enabled'] = best_params.get('slope_filter_enabled')
    best_config['signal']['slope_filter']['period'] = best_params.get('slope_period')
    best_config['signal']['slope_filter']['threshold'] = best_params.get('slope_threshold')
    best_config['volatility']['ewma_lambda'] = best_params.get('ewma_lambda')
    best_config['volatility']['dynamic_obi']['enabled'] = best_params.get('dynamic_obi_enabled')
    best_config['volatility']['dynamic_obi']['volatility_factor'] = best_params.get('volatility_factor')
    best_config['volatility']['dynamic_obi']['min_threshold_factor'] = best_params.get('min_threshold_factor')
    best_config['volatility']['dynamic_obi']['max_threshold_factor'] = best_params.get('max_threshold_factor')
    best_config['twap']['enabled'] = best_params.get('twap_enabled')
    best_config['twap']['max_order_size_btc'] = best_params.get('twap_max_order_size_btc')
    best_config['twap']['interval_seconds'] = best_params.get('twap_interval_seconds')
    best_config['twap']['partial_exit_enabled'] = best_params.get('twap_partial_exit_enabled')
    best_config['twap']['profit_threshold'] = best_params.get('twap_profit_threshold')
    best_config['twap']['exit_ratio'] = best_params.get('twap_exit_ratio')
    best_config['risk']['max_drawdown_percent'] = best_params.get('risk_max_drawdown_percent')
    best_config['risk']['max_position_ratio'] = best_params.get('risk_max_position_ratio')

    with open('config/best_trade_config.yaml', 'w') as f:
        yaml.dump(best_config, f)

    print("\nBest trade config saved to config/best_trade_config.yaml")
