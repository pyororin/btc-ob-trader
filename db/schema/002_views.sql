-- v_pnl_with_benchmark ビュー
-- pnl_summaryテーブルとbenchmark_valuesテーブルを結合し、
-- 正規化されたパフォーマンスを比較するためのビュー
CREATE OR REPLACE VIEW v_pnl_with_benchmark AS
WITH pnl_normalized AS (
    -- 各 strategy_id の最初の total_pnl を取得
    SELECT
        time,
        strategy_id,
        total_pnl,
        first_value(total_pnl) OVER (PARTITION BY strategy_id ORDER BY time ASC) as first_pnl
    FROM
        pnl_summary
), benchmark_normalized AS (
    -- 各 strategy_id の最初の value を取得
    SELECT
        time,
        strategy_id,
        value,
        first_value(value) OVER (PARTITION BY strategy_id ORDER BY time ASC) as first_value
    FROM
        benchmark_values
)
-- PnLとベンチマークを100を基準に正規化して結合
SELECT
    p.time,
    p.strategy_id,
    (1 + (p.total_pnl - p.first_pnl) / p.first_pnl) * 100 AS normalized_pnl,
    (1 + (b.value - b.first_value) / b.first_value) * 100 AS normalized_benchmark
FROM
    pnl_normalized p
LEFT JOIN
    benchmark_normalized b ON time_bucket('1 minute', p.time) = time_bucket('1 minute', b.time) AND p.strategy_id = b.strategy_id
ORDER BY
    p.time;
