local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local row = grafana.row;
local graphPanel = grafana.graphPanel;
local table = grafana.table;
local template = grafana.template;
local text = grafana.text;

local commonTarget(query) = {
  datasource: 'TimescaleDB',
  format: 'table',
  rawSql: query,
  refId: std.strSubst('%s', [std.uuid()]),
};

local notePanel =
  text.new(
    title='NOTE: How to Read This Dashboard',
    mode='markdown',
    content=|||
      ### ダッシュボードの読み方

      このダッシュボードは、開発したボットのパフォーマンスを市場のベンチマーク（単純な買い持ち戦略）と比較するためのものです。

      ---

      #### **PnL vs. Benchmark (Normalized)**
      - **目的**: ボットの累積損益（`last_pnl`）と、正規化されたベンチマーク価格（`normalized_price`）を比較します。
      - **見方**:
        - `last_pnl` (青線): ボットが生み出した実際の累積損益です。
        - `normalized_price` (黄線): 期間開始時点の価格を100とした場合の、市場価格の推移です。
      - **判断**:
        - **青線が黄線を上回っている場合**: ボットは市場平均（買い持ち戦略）よりも優れたパフォーマンスを上げています。
        - **青線が黄線を下回っている場合**: ボットのパフォーマンスは市場平均に劣っています。

      ---

      #### **Performance Alpha**
      - **目的**: ボットのパフォーマンスがベンチマークをどれだけ上回ったか（または下回ったか）を示します。
      - **計算式**: `Alpha = last_pnl - normalized_price`
      - **見方**:
        - `alpha` (緑のエリア): PnLと正規化ベンチマークの差です。
      - **判断**:
        - **Alphaがプラス（0以上）**: ボットはベンチマークを上回る超過リターン（アルファ）を生み出しています。
        - **Alphaがマイナス（0未満）**: ボットはベンチマークを下回るパフォーマンスしか出せていません。

      > **総括**: 長期的に **Alphaがプラス圏で安定または右肩上がり** になることが、この戦略が有効であると判断する上での重要な指標となります。
    |||
  );

dashboard.new(
  'Performance vs. Benchmark',
  description='Compares bot PnL against a buy-and-hold benchmark.',
  tags=['performance', 'benchmark'],
  timezone='browser',
)
.addPanel(notePanel, gridPos={ x: 0, y: 0, w: 24, h: 8 })
.addPanel(
  graphPanel.new(
    'PnL vs. Benchmark (Normalized)',
    datasource='TimescaleDB',
    description='Bot Total PnL vs. Normalized Benchmark Price (starts at 100).',
  )
  .addTarget(
    commonTarget('SELECT bucket AS "time", last_pnl FROM v_performance_vs_benchmark ORDER BY bucket')
  )
  .addTarget(
    commonTarget('SELECT bucket AS "time", normalized_price FROM v_performance_vs_benchmark ORDER BY bucket')
  ),
  gridPos={ x: 0, y: 8, w: 24, h: 12 }
)
.addPanel(
  graphPanel.new(
    'Performance Alpha',
    datasource='TimescaleDB',
    description='Difference between PnL and normalized benchmark.',
  )
  .addTarget(
    commonTarget('SELECT bucket AS "time", alpha FROM v_performance_vs_benchmark ORDER BY bucket')
  ),
  gridPos={ x: 0, y: 20, w: 24, h: 12 }
)
