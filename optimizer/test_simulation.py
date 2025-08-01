import unittest
from unittest.mock import patch, MagicMock, mock_open
import yaml
from pathlib import Path
import os

# Assume optimizer.config is setup correctly
from optimizer import config
from optimizer.simulation import run_simulation

# This is the full content of the actual template file.
# Using the real template makes the test much more robust.
FULL_TEMPLATE_CONTENT = """
# OBI-Scalp-Bot 取引戦略設定ファイル (trade_config.yaml)
#
# このファイルは、ボットの取引ロジックに直接関わるパラメータを管理します。
# 市場の状況やバックテストの結果に応じて、これらの値を調整してください。
#
# この .template ファイルは optimizer.py によって使用されます。
# {{ parameter_name }} の形式のプレースホルダーは、最適化中に
# Optuna によって動的に置き換えられます。

# ------------------------------------------------------------------------------
# 基本取引設定 (Basic Trading)
# ------------------------------------------------------------------------------

# pair: 取引対象の通貨ペア。
# 例: "btc_jpy", "eth_jpy"
pair: "btc_jpy"

# order_amount: 1回の取引で発注する量。
# 例: 0.01
order_amount: 0.01

# spread_limit: 取引を許可する最大のスプレッド（最良買値と最良売値の差）を円で指定します。
# これよりスプレッドが広い場合、ボットは新規エントリーを見送ります。
# 市場の流動性が低い時や急変動時に、不利な価格で約定するのを防ぐためのフィルターです。
spread_limit: {{ spread_limit }}

# ------------------------------------------------------------------------------
# ポジションサイズ設定 (Position Sizing)
# ------------------------------------------------------------------------------

# lot_max_ratio: 1回の注文で使用する証拠金の最大比率。
# 例えば、0.05 は利用可能残高の5%を上限とすることを意味します。
# リスク管理の重要な要素です。値を大きくするとハイリスク・ハイリターンになります。
lot_max_ratio: {{ lot_max_ratio }}

# order_ratio: 注文量に対する比率。
# この比率は、アダプティブ・ポジションサイジング機能によって動的に調整されることがあります。
order_ratio: {{ order_ratio }}

# adaptive_position_sizing: アダプティブ（適応型）ポジションサイズ調整機能の設定。
# 直近の取引成績に応じて、自動でロットサイズを調整します。
adaptive_position_sizing:
  # enabled: この機能を有効にするか (`true`) 無効にするか (`false`)。
  enabled: {{ adaptive_position_sizing.enabled }}
  # num_trades: パフォーマンス評価の対象とする直近の取引回数。
  num_trades: {{ adaptive_position_sizing.num_trades }}
  # reduction_step: 損失が出た場合に、次の注文のロットサイズを縮小する係数。
  # 0.8 は、現在のロットサイズを80%に減らすことを意味します（20%減）。
  reduction_step: {{ adaptive_position_sizing.reduction_step }}
  # min_ratio:ロットサイズの下限を、元の `order_ratio` に対する割合で指定します。
  # 0.5 は、`order_ratio` の50%より小さくならないように制限します。
  min_ratio: {{ adaptive_position_sizing.min_ratio }}

# ------------------------------------------------------------------------------
# エントリー戦略 (Entry Strategy)
# ------------------------------------------------------------------------------

# long: ロング（買い）エントリーの戦略設定。
long:
  # obi_threshold: ロングエントリーをトリガーするOBI (Order Book Imbalance) の閾値。
  # OBIがこの値を超えると、買いシグナルの候補となります。正の値を指定します。
  obi_threshold: {{ long.obi_threshold }}
  # tp: 利食い（Take Profit）を行う価格幅を円で指定します。
  # エントリー価格からこの値幅分、価格が上昇した場合に利食い注文が執行されます。
  tp: {{ long.tp }}
  # sl: 損切り（Stop Loss）を行う価格幅を円で指定します。
  # エントリー価格からこの値幅分、価格が下落した場合に損切り注文が執行されます。
  # 必ず負の値を指定してください。
  sl: {{ long.sl }}

# short: ショート（売り）エントリーの戦略設定。
short:
  # obi_threshold: ショートエントリーをトリガーするOBIの閾値。
  # OBIがこの値を下回ると（より負の方向に大きいと）、売りシグナルの候補となります。負の値を指定します。
  obi_threshold: {{ short.obi_threshold }}
  # tp: 利食い（Take Profit）を行う価格幅を円で指定します。
  # エントリー価格からこの値幅分、価格が下落した場合に利食い注文が執行されます。
  tp: {{ short.tp }}
  # sl: 損切り（Stop Loss）を行う価格幅を円で指定します。
  # エントリー価格からこの値幅分、価格が上昇した場合に損切り注文が執行されます。
  # 必ず負の値を指定してください（例: -20 は20円の上昇で損切り）。
  sl: {{ short.sl }}

# ------------------------------------------------------------------------------
# シグナルフィルター (Signal Filters)
# ------------------------------------------------------------------------------

# signal: シグナルの精度を高めるためのフィルター設定。
signal:
  # hold_duration_ms: シグナル確定までの待機時間（ミリ秒）。
  # OBIが閾値を超えても即座にエントリーせず、この時間だけ閾値を超え続けた場合にシグナルが確定します。
  # 値を大きくすると「ダマシ」を避けやすくなりますが、エントリーは遅れます。
  # 値を小さくすると素早く反応できますが、ノイズに弱くなります。
  hold_duration_ms: {{ signal.hold_duration_ms }}

  # cvd_window_minutes: CVD (Cumulative Volume Delta) を計算する際の期間（分）。
  cvd_window_minutes: 1

  # --- 複合シグナル設定 (Composite Signal Settings) ---
  # 以下のパラメータは、複数の指標を組み合わせて取引シグナルを生成するために使用されます。
  # 各指標に重みを付け、その合計スコアが `composite_threshold` を超えた場合にシグナルが発生します。

  # obi_weight: OBI (Order Book Imbalance) の重み。
  obi_weight: {{ signal.obi_weight }}
  # ofi_weight: OFI (Order Flow Imbalance) の重み。
  ofi_weight: {{ signal.ofi_weight }}
  # cvd_weight: CVD (Cumulative Volume Delta) の重み。
  cvd_weight: {{ signal.cvd_weight }}
  # micro_price_weight: MicroPrice の変化の重み。
  micro_price_weight: {{ signal.micro_price_weight }}
  # composite_threshold: 複合シグナルの発動閾値。
  composite_threshold: {{ signal.composite_threshold }}

  # slope_filter: OBIの傾き（変化率）を利用したフィルター設定。
  # OBIの値だけでなく、その変化の勢いも考慮に入れることで、トレンドの初動を捉えやすくなります。
  slope_filter:
    # enabled: このフィルターを有効にするか (`true`) 無効にするか (`false`)。
    enabled: {{ signal.slope_filter.enabled }}
    # period: 傾きを計算するために使用する、直近のOBIデータの数。
    period: {{ signal.slope_filter.period }}
    # threshold: エントリーを許可するOBIの最小傾き。
    # 例えば、ロングシグナルの場合、OBIの傾きがこの値より大きい必要があります。
    threshold: {{ signal.slope_filter.threshold }}

# ------------------------------------------------------------------------------
# 動的パラメータ調整 (Dynamic Parameters)
# ------------------------------------------------------------------------------

# volatility: 市場のボラティリティ（価格変動の大きさ）に関する設定。
volatility:
  # ewma_lambda: ボラティリティ計算に使用するEWMA（指数平滑移動平均）のλ（ラムダ）値。
  # 0に近いほど過去の値を重視し、1に近いほど直近の値を重視します。
  ewma_lambda: {{ volatility.ewma_lambda }}
  # dynamic_obi: ボラティリティに応じてOBIの閾値を動的に調整する機能。
  dynamic_obi:
    # enabled: この機能を有効にするか (`true`) 無効にするか (`false`)。
    # trueにすると、ボラティリティが高い時は閾値を広げ、低い時は狭めるよう自動調整します。
    enabled: {{ volatility.dynamic_obi.enabled }}
    # volatility_factor: ボラティリティをOBI閾値の調整にどれだけ反映させるかの係数。
    volatility_factor: {{ volatility.dynamic_obi.volatility_factor }}
    # min_threshold_factor: 動的閾値の下限を、元のOBI閾値に対する割合で指定します。
    min_threshold_factor: {{ volatility.dynamic_obi.min_threshold_factor }}
    # max_threshold_factor: 動的閾値の上限を、元のOBI閾値に対する割合で指定します。
    max_threshold_factor: {{ volatility.dynamic_obi.max_threshold_factor }}

# ------------------------------------------------------------------------------
# 執行戦略 (Execution Strategy)
# ------------------------------------------------------------------------------

# twap: TWAP (Time-Weighted Average Price) 注文の設定。
# 大きな注文を時間で分割して発注し、市場への価格インパクトを抑えつつ平均取得単価を安定させます。
twap:
  # enabled: TWAP注文を有効にするか (`true`) 無効にするか (`false`)。
  enabled: {{ twap.enabled }}
  # max_order_size_btc: 1回の分割注文における最大のサイズ（BTC）。
  # 最小値は取引所の制約により 0.01 です。
  max_order_size_btc: {{ twap.max_order_size_btc }}
  # interval_seconds: 分割された注文を発注する間隔（秒）。
  interval_seconds: {{ twap.interval_seconds }}
  # partial_exit_enabled: ポジションの一部を利益確定する（部分利食い）機能を有効にするか。
  # この機能は現在、TWAP注文と連動しています。
  partial_exit_enabled: {{ twap.partial_exit_enabled }}
  # profit_threshold: 部分利食いをトリガーする利益率（%）。
  # 例: 0.5 は、0.5%の含み益が出た場合にトリガーされます。
  profit_threshold: {{ twap.profit_threshold }}
  # exit_ratio: 部分利食いするポジションの割合。
  # 例: 0.5 は、現在のポジションの50%を利益確定します。
  exit_ratio: {{ twap.exit_ratio }}

# ------------------------------------------------------------------------------
# リスク管理 (Risk Management)
# ------------------------------------------------------------------------------

risk:
  # max_drawdown_percent: 許容する最大のドローダウン率（%）。
  # 資産のピークからの下落率がこの値を超えた場合、ボットは新規の注文を停止します。
  # 非常に重要なリスク管理項目です。
  max_drawdown_percent: {{ risk.max_drawdown_percent }}
  # max_position_ratio: 利用可能残高に対する許容される最大ポジションサイズの割合。
  # 1.0 は、残高の100%までポジションを持つことを意味します。
  # これにより、所持金を超える取引を防ぎます。
  max_position_ratio: {{ risk.max_position_ratio }}
"""

class TestSimulation(unittest.TestCase):

    # Patch the config paths to ensure they are controlled for this test
    @patch('optimizer.config.CONFIG_TEMPLATE_PATH', Path("/mock/template.yaml"))
    @patch('optimizer.config.SIMULATION_BINARY_PATH', Path("/mock/bin/bot"))
    @patch('optimizer.config.APP_ROOT', Path("/mock/app"))
    @patch('optimizer.config.PARAMS_DIR', Path("/mock/params"))
    @patch('optimizer.simulation.tempfile.NamedTemporaryFile')
    @patch('optimizer.simulation.subprocess.run')
    @patch('optimizer.simulation.os.remove')
    def test_run_simulation_with_nested_params(self, mock_os_remove, mock_subprocess_run, mock_tempfile):
        """
        Test that run_simulation correctly renders a NESTED parameter dictionary
        into a YAML file for the Go simulation. This confirms that the template
        and the parameter structure are compatible.
        """
        # 1. Define a correctly nested parameter dictionary
        nested_params = {
            "spread_limit": 50, "lot_max_ratio": 0.9, "order_ratio": 0.95,
            "adaptive_position_sizing": { "enabled": True, "num_trades": 10, "reduction_step": 0.8, "min_ratio": 0.5 },
            "long": { "obi_threshold": 1.5, "tp": 100, "sl": -100 },
            "short": { "obi_threshold": -1.5, "tp": 100, "sl": -100 },
            "signal": { "hold_duration_ms": 500, "obi_weight": 1.0, "ofi_weight": 0.5, "cvd_weight": 0.2, "micro_price_weight": 0.8, "composite_threshold": 1.8,
                "slope_filter": { "enabled": False, "period": 10, "threshold": 0.1 } },
            "volatility": { "ewma_lambda": 0.1,
                "dynamic_obi": { "enabled": True, "volatility_factor": 2.0, "min_threshold_factor": 0.7, "max_threshold_factor": 1.5 } },
            "twap": { "enabled": False, "max_order_size_btc": 0.05, "interval_seconds": 5, "partial_exit_enabled": False, "profit_threshold": 1.0, "exit_ratio": 0.5 },
            "risk": { "max_drawdown_percent": 20, "max_position_ratio": 0.8 }
        }

        # 2. Mock the subprocess to prevent actual execution
        mock_process_result = MagicMock()
        mock_process_result.stdout = '{"TotalTrades": 10, "SharpeRatio": 1.5}'
        mock_subprocess_run.return_value = mock_process_result

        # 3. Mock the temporary file to capture the written content
        mock_file_handle = mock_open()
        # The file object returned by the context manager needs a 'name' attribute
        # for the `Path(temp_f.name)` call inside the function under test.
        file_object_mock = mock_file_handle.return_value
        file_object_mock.name = '/mock/params/dummy_temp_file.yaml'
        mock_tempfile.return_value.__enter__.return_value = file_object_mock

        # Mock the open call for the *template* file using the full template content
        with patch('builtins.open', mock_open(read_data=FULL_TEMPLATE_CONTENT)):
            # 4. Call the function to be tested
            run_simulation(params=nested_params, sim_csv_path=Path("dummy.csv"))

        # 5. Assert that the content written to the temp file is correct
        # The 'write' method is on the file object mock, not the mock_open return value.
        written_yaml_str = "".join(call.args[0] for call in file_object_mock.write.call_args_list)
        written_data = yaml.safe_load(written_yaml_str)

        # Check a few key values from different nested sections
        self.assertEqual(written_data['spread_limit'], 50)
        self.assertTrue(written_data['adaptive_position_sizing']['enabled'])
        self.assertEqual(written_data['long']['tp'], 100)
        self.assertEqual(written_data['signal']['slope_filter']['period'], 10)
        self.assertEqual(written_data['risk']['max_position_ratio'], 0.8)
        self.assertEqual(written_data['volatility']['dynamic_obi']['volatility_factor'], 2.0)

        # Verify that the subprocess was called correctly
        self.assertTrue(mock_subprocess_run.called)

if __name__ == '__main__':
    unittest.main()
