local grafana = import 'github.com/grafana/grafonnet-lib/grafonnet/grafana.libsonnet';
local dashboard = grafana.dashboard;
local row = grafana.row;
local graphPanel = grafana.graphPanel;
local table = grafana.table;

dashboard.new(
  'Performance vs. Benchmark',
  description='Compares bot PnL against a buy-and-hold benchmark.',
  tags=['performance', 'benchmark'],
  timezone='browser',
)
.addPanel(
  graphPanel.new(
    'PnL vs. Benchmark (Normalized)',
    datasource='TimescaleDB (Production)',
    description='Bot Total PnL vs. Normalized Benchmark Price (starts at 100).',
  )
  .addTarget(
    grafana.prometheus.target(
      'SELECT bucket AS time, last_pnl FROM v_performance_vs_benchmark ORDER BY bucket',
      format='time_series',
      legendFormat='Bot PnL',
    )
  )
  .addTarget(
    grafana.prometheus.target(
      'SELECT bucket AS time, normalized_price FROM v_performance_vs_benchmark ORDER BY bucket',
      format='time_series',
      legendFormat='Benchmark (Normalized)',
    )
  ),
  gridPos={ x: 0, y: 0, w: 24, h: 12 }
)
.addPanel(
  graphPanel.new(
    'Performance Alpha',
    datasource='TimescaleDB (Production)',
    description='Difference between PnL and normalized benchmark.',
  )
  .addTarget(
    grafana.prometheus.target(
      'SELECT bucket AS time, alpha FROM v_performance_vs_benchmark ORDER BY bucket',
      format='time_series',
      legendFormat='Alpha',
    )
  ),
  gridPos={ x: 0, y: 12, w: 24, h: 12 }
)
