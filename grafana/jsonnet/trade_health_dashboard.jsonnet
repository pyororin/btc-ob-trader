local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local graph = grafana.graphPanel;
local singlestat = grafana.singlestat;
local tablePanel = grafana.tablePanel;
local text = grafana.text;

local commonTarget(query) = {
  datasource: 'TimescaleDB',
  format: 'time_series',
  rawSql: query,
  refId: std.strSubst('%s', [std.uuid()]),
};

local notePanel =
  text.new(
    title='NOTE: How to Read This Dashboard',
    mode='markdown',
    content=|||
      ### ダッシュボードの読み方

      このダッシュボードは、取引の健全性を監視するためのものです。

      ---

      #### **Transaction Volume**
      - **目的**: 時間帯別の取引回数と取引総額を監視します。
      - **見方**:
        - **Y軸**: 取引回数または取引総額
        - **X軸**: 時刻
      - **判断**:
        - **通常時**: 取引量が安定していることを確認します。
        - **異常時**: 取引量が急増または急減していないかを確認します。

      #### **Success Rate**
      - **目的**: 正常に完了した取引とエラーになった取引の割合を監視します。
      - **見方**:
        - 正常な取引の割合が常に高い状態（例: 99%以上）であることを確認します。
      - **判断**:
        - **割合の低下**: エラー率が上昇している場合、システムに問題が発生している可能性があります。

      #### **Processing Time**
      - **目的**: 個々の取引の処理にかかった時間の平均や最大値を監視します。
      - **見方**:
        - 平均処理時間が一定の範囲内に収まっていることを確認します。
        - 最大処理時間に大きなスパイクがないかを確認します。
      - **判断**:
        - **処理時間の増加**: システムのパフォーマンスが低下している可能性があります。

      #### **Error Breakdown**
      - **目的**: エラーの種類別の発生件数を監視します。
      - **見方**:
        - 特定のエラーが頻発していないかを確認します。
      - **判断**:
        - **特定エラーの多発**: そのエラーの原因を調査する必要があります。
    |||
  );

dashboard.new(
  'Trade Health Dashboard',
  description='Monitors the health of trading operations.',
  tags=['trading', 'health'],
  timezone='browser',
)
.addPanel(notePanel, gridPos={ x: 0, y: 0, w: 24, h: 8 })
.addPanel(
  graph.new(
    'Transaction Volume',
    datasource='TimescaleDB',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        time_bucket('5 minutes', "time") AS "time",
        count(*) AS "volume"
      FROM trades
      GROUP BY 1
      ORDER BY 1
    |||
    )
  ), gridPos={ x: 0, y: 8, w: 24, h: 8 }
)
.addPanel(
  singlestat.new(
    'Success Rate',
    datasource='TimescaleDB',
    valueName='current',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        (CAST(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END) AS REAL) / COUNT(*)) * 100
      FROM trades
    |||
    )
  ), gridPos={ x: 0, y: 16, w: 12, h: 8 }
)
.addPanel(
  graph.new(
    'Processing Time (ms)',
    datasource='TimescaleDB',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        time_bucket('5 minutes', "time") AS "time",
        avg(processing_time_ms) AS "avg_processing_time"
      FROM trades
      GROUP BY 1
      ORDER BY 1
    |||
    )
  ), gridPos={ x: 12, y: 16, w: 12, h: 8 }
)
.addPanel(
  tablePanel.new(
    'Error Breakdown',
    datasource='TimescaleDB',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        error_type,
        count(*) AS "count"
      FROM trades
      WHERE status = 'error'
      GROUP BY 1
      ORDER BY 2 DESC
    |||
    )
  ), gridPos={ x: 0, y: 24, w: 24, h: 8 }
)
