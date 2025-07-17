-- パフォーマンスとベンチマークを比較するためのビュー
CREATE OR REPLACE VIEW v_performance_vs_benchmark AS
WITH pnl_binned AS (
    -- PnLデータを1分間隔で集約
    SELECT
        time_bucket('1 minute', time) AS bucket,
        last(total_pnl, time) AS last_pnl
    FROM pnl_summary
    GROUP BY bucket
),
benchmark_binned AS (
    -- ベンチマークデータを1分間隔で集約
    SELECT
        time_bucket('1 minute', time) AS bucket,
        -- 最初の価格を基準点とする
        first(price, time) as first_price,
        last(price, time) AS last_price
    FROM benchmark_values
    GROUP BY bucket
),
-- ベンチマークの正規化
normalized_benchmark AS (
    SELECT
        bucket,
        last_price,
        -- 全期間での最初の価格を取得して、それで正規化する
        last_price / first(first_price, bucket) OVER (ORDER BY bucket) * 100 AS normalized_price
    FROM benchmark_binned
)
-- PnLと正規化されたベンチマークを結合
SELECT
    p.bucket,
    p.last_pnl,
    b.normalized_price,
    -- PnLから正規化ベンチマーク価格を引いてアルファを計算（単純化）
    -- 実際のPnLはJPY建て、ベンチマークは正規化されているため、比較の仕方は要検討
    -- ここでは例として単純な差分を表示
    p.last_pnl - b.normalized_price AS alpha
FROM pnl_binned p
JOIN normalized_benchmark b ON p.bucket = b.bucket
ORDER BY p.bucket;
