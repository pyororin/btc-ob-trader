local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local row = grafana.row;
local tablePanel = grafana.tablePanel;
local graph = grafana.graphPanel;
local singlestat = grafana.singlestat;
local prometheus = grafana.prometheus;

dashboard.new(
  'Optimizer Performance',
  tags=['optimizer', 'bot'],
  timezone='browser',
  time_from='now-7d',
)
.addTemplate(
  grafana.template.datasource(
    'DS_PROMETHEUS',
    'prometheus',
    'Prometheus',
    hide='label',
  )
)
.addTemplate(
  grafana.template.new(
    'interval',
    '${DS_PROMETHEUS}',
    '1m,5m,10m,30m,1h,6h,12h',
    'Interval',
    '1h',
    multi=false,
    includeAll=false,
  )
)
.addRow(
  row.new()
  .addPanel(
    graph.new(
      'OOS Performance Over Time',
      datasource='TimescaleDB',
      description='Out-of-Sample (OOS) Profit Factor and Sharpe Ratio for each optimization run.',
    )
    .addTarget(
      grafana.sql.target(
        'SELECT time, oos_profit_factor AS "Profit Factor" FROM optimization_history WHERE $__timeFilter(time) ORDER BY time',
        format='time_series',
      ) { refId: 'A' }
    )
    .addTarget(
      grafana.sql.target(
        'SELECT time, oos_sharpe_ratio AS "Sharpe Ratio" FROM optimization_history WHERE $__timeFilter(time) ORDER BY time',
        format='time_series',
      ) { refId: 'B' }
    )
  )
  .addPanel(
    singlestat.new(
      'Successful Optimizations (24h)',
      datasource='TimescaleDB',
      description='Number of successful (passed OOS validation) optimizations in the last 24 hours.',
      valueName='current',
    )
    .addTarget(
      grafana.sql.target(
        "SELECT count(*) FROM optimization_history WHERE validation_passed = true AND time > NOW() - INTERVAL '24 hours'",
        format='table',
      )
    )
  )
)
.addRow(
  row.new()
  .addPanel(
    tablePanel.new(
      'Optimization History',
      datasource='TimescaleDB',
      description='Detailed log of all optimization runs.',
    )
    .addTarget(
      grafana.sql.target(
        'SELECT time, trigger_type, is_hours, oos_hours, is_profit_factor, oos_profit_factor, oos_sharpe_ratio, validation_passed, best_params FROM optimization_history WHERE $__timeFilter(time) ORDER BY time DESC',
        format='table',
      )
    )
  )
)
