local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local row = grafana.row;
local graphPanel = grafana.graphPanel;
local table = grafana.table;
local template = grafana.template;

local commonTarget(query) = {
  datasource: 'TimescaleDB',
  format: 'table',
  rawSql: query,
  refId: std.strSubst('%s', [std.uuid()]),
};

dashboard.new(
  'Performance vs. Benchmark',
  description='Compares bot PnL against a buy-and-hold benchmark.',
  tags=['performance', 'benchmark'],
  timezone='browser',
)
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
  gridPos={ x: 0, y: 0, w: 24, h: 12 }
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
  gridPos={ x: 0, y: 12, w: 24, h: 12 }
)
