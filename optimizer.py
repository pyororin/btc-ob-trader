import yaml
import optuna
import subprocess
import os
import re
import json

def objective(trial):
    # 1. パラメータ空間の定義（拡張版）
    params = {
        'spread_limit': trial.suggest_int('spread_limit', 10, 150),
        'lot_max_ratio': trial.suggest_float('lot_max_ratio', 0.01, 0.2),
        'order_ratio': trial.suggest_float('order_ratio', 0.05, 0.25),
        'adaptive_position_sizing': {
            'enabled': trial.suggest_categorical('adaptive_position_sizing_enabled', [True, False]),
            'num_trades': trial.suggest_int('adaptive_num_trades', 3, 20),
            'reduction_step': trial.suggest_float('adaptive_reduction_step', 0.5, 1.0),
            'min_ratio': trial.suggest_float('adaptive_min_ratio', 0.1, 0.8),
        },
        'long': {
            'obi_threshold': trial.suggest_float('long_obi_threshold', 0.1, 2.0),
            'tp': trial.suggest_int('long_tp', 50, 500),
            'sl': trial.suggest_int('long_sl', -500, -50),
        },
        'short': {
            'obi_threshold': trial.suggest_float('short_obi_threshold', -2.0, -0.1),
            'tp': trial.suggest_int('short_tp', 50, 500),
            'sl': trial.suggest_int('short_sl', -500, -50),
        },
        'signal': {
            'hold_duration_ms': trial.suggest_int('hold_duration_ms', 100, 2000),
            'slope_filter': {
                'enabled': trial.suggest_categorical('slope_filter_enabled', [True, False]),
                'period': trial.suggest_int('slope_period', 3, 50),
                'threshold': trial.suggest_float('slope_threshold', 0.0, 0.5),
            }
        },
        'volatility': {
            'ewma_lambda': trial.suggest_float('ewma_lambda', 0.05, 0.3),
            'dynamic_obi': {
                'enabled': trial.suggest_categorical('dynamic_obi_enabled', [True, False]),
                'volatility_factor': trial.suggest_float('volatility_factor', 0.5, 5.0),
                'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.5, 1.0),
                'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.0, 3.0),
            }
        },
        'twap': {
            'enabled': trial.suggest_categorical('twap_enabled', [True, False]),
            'max_order_size_btc': trial.suggest_float('twap_max_order_size_btc', 0.01, 0.1),
            'interval_seconds': trial.suggest_int('twap_interval_seconds', 1, 10),
            'partial_exit_enabled': trial.suggest_categorical('twap_partial_exit_enabled', [True, False]),
            'profit_threshold': trial.suggest_float('twap_profit_threshold', 0.1, 2.0),
            'exit_ratio': trial.suggest_float('twap_exit_ratio', 0.1, 1.0),
        },
        'risk': {
            'max_drawdown_percent': trial.suggest_int('risk_max_drawdown_percent', 5, 20),
            'max_position_jpy': trial.suggest_float('risk_max_position_jpy', 100000, 1000000),
        }
    }

    # 2. trade_config.yaml の読み込みと更新
    with open('config/trade_config.yaml', 'r') as f:
        trade_config = yaml.safe_load(f)

    # パラメータを trade_config に設定
    trade_config['spread_limit'] = params['spread_limit']
    trade_config['lot_max_ratio'] = params['lot_max_ratio']
    trade_config['order_ratio'] = params['order_ratio']
    trade_config['adaptive_position_sizing']['enabled'] = params['adaptive_position_sizing']['enabled']
    trade_config['adaptive_position_sizing']['num_trades'] = params['adaptive_position_sizing']['num_trades']
    trade_config['adaptive_position_sizing']['reduction_step'] = params['adaptive_position_sizing']['reduction_step']
    trade_config['adaptive_position_sizing']['min_ratio'] = params['adaptive_position_sizing']['min_ratio']
    trade_config['long']['obi_threshold'] = params['long']['obi_threshold']
    trade_config['long']['tp'] = params['long']['tp']
    trade_config['long']['sl'] = params['long']['sl']
    trade_config['short']['obi_threshold'] = params['short']['obi_threshold']
    trade_config['short']['tp'] = params['short']['tp']
    trade_config['short']['sl'] = params['short']['sl']
    trade_config['signal']['hold_duration_ms'] = params['signal']['hold_duration_ms']
    trade_config['signal']['slope_filter']['enabled'] = params['signal']['slope_filter']['enabled']
    trade_config['signal']['slope_filter']['period'] = params['signal']['slope_filter']['period']
    trade_config['signal']['slope_filter']['threshold'] = params['signal']['slope_filter']['threshold']
    trade_config['volatility']['ewma_lambda'] = params['volatility']['ewma_lambda']
    trade_config['volatility']['dynamic_obi']['enabled'] = params['volatility']['dynamic_obi']['enabled']
    trade_config['volatility']['dynamic_obi']['volatility_factor'] = params['volatility']['dynamic_obi']['volatility_factor']
    trade_config['volatility']['dynamic_obi']['min_threshold_factor'] = params['volatility']['dynamic_obi']['min_threshold_factor']
    trade_config['volatility']['dynamic_obi']['max_threshold_factor'] = params['volatility']['dynamic_obi']['max_threshold_factor']
    trade_config['twap']['enabled'] = params['twap']['enabled']
    trade_config['twap']['max_order_size_btc'] = params['twap']['max_order_size_btc']
    trade_config['twap']['interval_seconds'] = params['twap']['interval_seconds']
    trade_config['twap']['partial_exit_enabled'] = params['twap']['partial_exit_enabled']
    trade_config['twap']['profit_threshold'] = params['twap']['profit_threshold']
    trade_config['twap']['exit_ratio'] = params['twap']['exit_ratio']
    trade_config['risk']['max_drawdown_percent'] = params['risk']['max_drawdown_percent']
    trade_config['risk']['max_position_jpy'] = params['risk']['max_position_jpy']

    # 更新した設定を一時ファイルに保存
    temp_config_path = f'config/trade_config_trial_{trial.number}.yaml'
    with open(temp_config_path, 'w') as f:
        yaml.dump(trade_config, f)

    # 3. シミュレーションの実行
    csv_path = os.getenv('CSV_PATH')
    if not csv_path:
        raise ValueError("CSV_PATH environment variable is not set.")

    # ZIPファイルが指定されているか確認し、展開する
    unzipped_csv_path = csv_path
    if csv_path.endswith('.zip'):
        import zipfile
        simulation_dir = 'simulation'
        os.makedirs(simulation_dir, exist_ok=True)
        with zipfile.ZipFile(csv_path, 'r') as zip_ref:
            zip_ref.extractall(simulation_dir)
        # 展開されたCSVファイル名を取得（ZIPファイル名から.zipを除いたものと仮定）
        unzipped_csv_path = os.path.join(simulation_dir, os.path.basename(csv_path)[:-4] + '.csv')

    # Dockerコンテナ内で実行するためのコマンドを構築
    # Note: コンテナ内のパスに変換
    container_csv_path = f"/simulation/{os.path.basename(unzipped_csv_path)}"
    # container_config_path は config/trade_config_trial_{trial.number}.yaml とする
    container_config_path = temp_config_path

    command = [
        'sudo', '-E', 'docker', 'compose', 'run', '--rm', '--no-deps',
        '-v', f"{os.path.abspath(unzipped_csv_path)}:{container_csv_path}",
        '-v', f"{os.path.abspath('config')}:/config",
        'bot-simulate',
        '--simulate',
        f'--csv={container_csv_path}',
        f'--trade-config={container_config_path}',
        '--json-output'
    ]

    print(f"Running command: {' '.join(command)}")
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout
        print(output)
        # Goプログラムが出力するJSONブロックを正規表現で探す
        json_match = re.search(r'^{.+}$', result.stdout, re.MULTILINE | re.DOTALL)
        if json_match:
            json_output = json_match.group(0)
            try:
                summary = json.loads(json_output)
                profit = summary.get('TotalProfit', 0.0)
            except json.JSONDecodeError:
                print(f"Failed to parse JSON: {json_output}")
                profit = 0.0
        else:
            # JSONが見つからない場合は、以前のフォールバックロジックを使用
            print("Could not find JSON block in output. Checking for text format.")
            profit_match = re.search(r'TotalProfit:\s*(-?[\d.]+)', output)
            if profit_match:
                profit = float(profit_match.group(1))
            else:
                print("Could not parse profit from output. Returning 0.")
                profit = 0.0
    except subprocess.CalledProcessError as e:
        print("Simulation failed.")
        print("--- STDOUT ---")
        print(e.stdout)
        print("--- STDERR ---")
        print(e.stderr)
        profit = 0.0 # 失敗した場合は最小値を返す

    # 5. 一時ファイルのクリーンアップ
    os.remove(temp_config_path)

    return profit

if __name__ == '__main__':
    n_trials = int(os.getenv('N_TRIALS', '100'))
    study_name = os.getenv('STUDY_NAME', 'obi-scalp-bot-optimization')
    storage_url = os.getenv('STORAGE_URL', 'sqlite:///optuna_study.db')

    study = optuna.create_study(
        study_name=study_name,
        storage=storage_url,
        load_if_exists=True,
        direction='maximize'
    )
    study.optimize(objective, n_trials=n_trials)

    print("Best trial:")
    trial = study.best_trial
    print(f"  Value: {trial.value}")
    print("  Params: ")
    for key, value in trial.params.items():
        print(f"    {key}: {value}")

    # 最適な設定をYAMLファイルとして出力
    best_params = trial.params
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
    best_config['risk']['max_position_jpy'] = best_params.get('risk_max_position_jpy')

    with open('config/best_trade_config.yaml', 'w') as f:
        yaml.dump(best_config, f)

    print("\nBest trade config saved to config/best_trade_config.yaml")
