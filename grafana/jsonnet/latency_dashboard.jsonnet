local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local heatmap = grafana.heatmapPanel;
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

      このダッシュボードは、注文約定のレイテンシ（ミリ秒単位）を監視するためのものです。

      ---

      #### **Latency Heatmap (p95/p99)**
      - **目的**: 5分間隔でのp95（95パーセンタイル）およびp99（99パーセンタイル）の約定レイテンシをヒートマップで視覚化します。
      - **見方**:
        - **Y軸**: レイテンシの範囲（ミリ秒）
        - **X軸**: 時刻
        - **色**: 各時間帯におけるレイテンシの発生頻度。色が明るいほど、そのレイテンシが頻繁に発生したことを示します。
      - **判断**:
        - **全体的に色が下に集中している**: ほとんどの取引が低いレイテンシで処理されており、システムは健全です。
        - **明るい色が上方にシフトしている**: レイテンシが悪化している兆候です。特にp99で高い値が頻発する場合、システムの一部にボトルネックが存在する可能性があります。
    |||
  );

dashboard.new(
  'Execution Latency Analysis',
  description='Analyzes order execution latency.',
  tags=['performance', 'latency'],
  timezone='browser',
)
.addPanel(notePanel, gridPos={ x: 0, y: 0, w: 24, h: 6 })
.addPanel(
  heatmap.new(
    'Latency Heatmap (p95)',
    datasource='TimescaleDB',
    dataFormat='tsbuckets',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        time_bucket('5 minutes', "time") AS "time",
        percentile_cont(0.95) WITHIN GROUP (ORDER BY latency_ms) AS "p95_latency"
      FROM latencies
      GROUP BY 1
      ORDER BY 1
    |||
    )
  ), gridPos={ x: 0, y: 6, w: 24, h: 10 }
)
.addPanel(
  heatmap.new(
    'Latency Heatmap (p99)',
    datasource='TimescaleDB',
    dataFormat='tsbuckets',
  )
  .addTarget(
    commonTarget( |||
      SELECT
        time_bucket('5 minutes', "time") AS "time",
        percentile_cont(0.99) WITHIN GROUP (ORDER BY latency_ms) AS "p99_latency"
      FROM latencies
      GROUP BY 1
      ORDER BY 1
    |||
    )
  ), gridPos={ x: 0, y: 16, w: 24, h: 10 }
)
