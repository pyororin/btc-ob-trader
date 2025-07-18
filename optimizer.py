import yaml
import optuna
import subprocess
import os
import re

def objective(trial):
    # 1. パラメータ空間の定義（拡張版）
    params = {
        'spread_limit': trial.suggest_int('spread_limit', 10, 100),
        'long': {
            'obi_threshold': trial.suggest_float('long_obi_threshold', 0.1, 2.0),
            'tp': trial.suggest_int('long_tp', 50, 300),
            'sl': trial.suggest_int('long_sl', -300, -50),
        },
        'short': {
            'obi_threshold': trial.suggest_float('short_obi_threshold', -2.0, -0.1),
            'tp': trial.suggest_int('short_tp', 50, 300),
            'sl': trial.suggest_int('short_sl', -300, -50),
        },
        'signal': {
            'hold_duration_ms': trial.suggest_int('hold_duration_ms', 100, 1500),
            'slope_filter': {
                'enabled': True,
                'period': trial.suggest_int('slope_period', 5, 30),
                'threshold': trial.suggest_float('slope_threshold', 0.0, 0.2),
            }
        },
        'volatility': {
            'dynamic_obi': {
                'volatility_factor': trial.suggest_float('volatility_factor', 1.0, 3.0),
                'min_threshold_factor': trial.suggest_float('min_threshold_factor', 0.5, 1.0),
                'max_threshold_factor': trial.suggest_float('max_threshold_factor', 1.0, 2.0),
            }
        }
    }

    # 2. trade_config.yaml の読み込みと更新
    with open('config/trade_config.yaml', 'r') as f:
        trade_config = yaml.safe_load(f)

    trade_config['spread_limit'] = params['spread_limit']
    trade_config['long']['obi_threshold'] = params['long']['obi_threshold']
    trade_config['long']['tp'] = params['long']['tp']
    trade_config['long']['sl'] = params['long']['sl']
    trade_config['short']['obi_threshold'] = params['short']['obi_threshold']
    trade_config['short']['tp'] = params['short']['tp']
    trade_config['short']['sl'] = params['short']['sl']
    trade_config['signal']['hold_duration_ms'] = params['signal']['hold_duration_ms']
    trade_config['signal']['slope_filter']['period'] = params['signal']['slope_filter']['period']
    trade_config['signal']['slope_filter']['threshold'] = params['signal']['slope_filter']['threshold']
    trade_config['volatility']['dynamic_obi']['volatility_factor'] = params['volatility']['dynamic_obi']['volatility_factor']
    trade_config['volatility']['dynamic_obi']['min_threshold_factor'] = params['volatility']['dynamic_obi']['min_threshold_factor']
    trade_config['volatility']['dynamic_obi']['max_threshold_factor'] = params['volatility']['dynamic_obi']['max_threshold_factor']

    # 更新した設定を一時ファイルに保存
    temp_config_path = f'config/trade_config_trial_{trial.number}.yaml'
    with open(temp_config_path, 'w') as f:
        yaml.dump(trade_config, f)

    # 3. シミュレーションの実行
    csv_path = os.getenv('CSV_PATH')
    if not csv_path:
        raise ValueError("CSV_PATH environment variable is not set.")

    # Goプログラムが --trade-config と --json-output フラグを受け取れるように修正したため、
    # それらのフラグを渡すように `docker-compose.yml` 経由でコマンドを組み立てる。
    # `Makefile` は直接使わず、`docker-compose` を直接呼び出すことで、
    # 引数のハンドリングを容易にする。
    # このため、あとで `docker-compose.yml` に `optimizer` サービスを追加する必要がある。
    command = [
        'sudo', '-E', 'docker', 'compose', 'run', '--rm', '--no-deps',
        'bot-simulate',
        '--simulate',
        f'--csv={csv_path}',
        f'--trade-config={temp_config_path}',
        '--json-output'
    ]

    print(f"Running command: {' '.join(command)}")
    try:
        result = subprocess.run(command, capture_output=True, text=True, check=True)
        output = result.stdout
        print(output)
        # JSON出力の最後の行をパースする
        try:
            summary = json.loads(output.strip().split('\n')[-1])
            profit = summary.get('TotalProfit', 0.0)
        except (json.JSONDecodeError, IndexError):
            print("Could not parse JSON from output. Checking for text format.")
            # JSONがダメならテキスト形式のフォールバックを試す
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

    # 5. 一時ファイルのクリーンアップ
    os.remove(temp_config_path)

    return profit

import json

if __name__ == '__main__':
    n_trials = int(os.getenv('N_TRIALS', '100'))
    study_name = os.getenv('STUDY_NAME', 'obi-scalp-bot-optimization')
    storage_url = os.getenv('STORAGE_URL', 'sqlite:///goptuna_study.db')

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

    best_config['spread_limit'] = best_params['spread_limit']
    best_config['long']['obi_threshold'] = best_params['long_obi_threshold']
    best_config['long']['tp'] = best_params['long_tp']
    best_config['long']['sl'] = best_params['long_sl']
    best_config['short']['obi_threshold'] = best_params['short_obi_threshold']
    best_config['short']['tp'] = best_params['short_tp']
    best_config['short']['sl'] = best_params['short_sl']
    best_config['signal']['hold_duration_ms'] = best_params['hold_duration_ms']
    best_config['signal']['slope_filter']['period'] = best_params['slope_period']
    best_config['signal']['slope_filter']['threshold'] = best_params['slope_threshold']
    best_config['volatility']['dynamic_obi']['volatility_factor'] = best_params['volatility_factor']
    best_config['volatility']['dynamic_obi']['min_threshold_factor'] = best_params['min_threshold_factor']
    best_config['volatility']['dynamic_obi']['max_threshold_factor'] = best_params['max_threshold_factor']

    with open('config/best_trade_config.yaml', 'w') as f:
        yaml.dump(best_config, f)

    print("\nBest trade config saved to config/best_trade_config.yaml")
