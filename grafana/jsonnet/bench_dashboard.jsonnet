local grafana = import 'grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local row = grafana.row;
local timeseries = grafana.timeseries;
local stat = grafana.stat;
local table = grafana.table;
local postgres = grafana.postgres;

dashboard.new(
  'Benchmark Dashboard',
  tags=['benchmark'],
  timezone='browser',
)
.addPanel(
  row.new(title='Overall Performance'),
)
.addPanel(
  stat.new(
    title='Normalized PnL',
    datasource='TimescaleDB (Production)',
    span=6,
  )
  .addTarget(
    postgres.target(
      rawSql='''
        SELECT
          time,
          normalized_pnl
        FROM v_pnl_with_benchmark
        WHERE
          strategy_id = 'default' AND $__timeFilter(time)
        ORDER BY 1
      '''
    )
  ),
)
.addPanel(
  stat.new(
    title='Normalized Benchmark',
    datasource='TimescaleDB (Production)',
    span=6,
  )
  .addTarget(
    postgres.target(
      rawSql='''
        SELECT
          time,
          normalized_benchmark
        FROM v_pnl_with_benchmark
        WHERE
          strategy_id = 'default' AND $__timeFilter(time)
        ORDER BY 1
      '''
    )
  ),
)
.addPanel(
  row.new(title='Performance Over Time'),
)
.addPanel(
  timeseries.new(
    title='PnL vs Benchmark Over Time',
    datasource='TimescaleDB (Production)',
    span=12,
  )
  .addTarget(
    postgres.target(
      rawSql='''
        SELECT
          time,
          normalized_pnl AS "PnL",
          normalized_benchmark AS "Benchmark"
        FROM v_pnl_with_benchmark
        WHERE
          strategy_id = 'default' AND $__timeFilter(time)
        ORDER BY 1
      '''
    )
  )
)
