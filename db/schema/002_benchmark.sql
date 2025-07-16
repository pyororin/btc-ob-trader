-- ベンチマーク結果テーブル (benchmark_results)
-- Buy & Holdなどの単純な戦略のパフォーマンスを記録
CREATE TABLE IF NOT EXISTS benchmark_results (
    time TIMESTAMPTZ NOT NULL,
    strategy_id TEXT NOT NULL, -- 例: 'buy_and_hold'
    pair TEXT NOT NULL,        -- 通貨ペア
    value DECIMAL NOT NULL     -- ベンチマーク戦略の評価額 (例: JPY)
);

-- benchmark_results テーブルをHypertableに変換
SELECT create_hypertable('benchmark_results', 'time', if_not_exists => TRUE);

-- benchmark_results テーブルの圧縮設定
ALTER TABLE benchmark_results SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'strategy_id, pair',
    timescaledb.compress_orderby = 'time DESC'
);
SELECT add_compression_policy('benchmark_results', INTERVAL '30 days', if_not_exists => TRUE);

-- インデックスの追加
CREATE INDEX IF NOT EXISTS idx_benchmark_results_strategy_pair_time ON benchmark_results (strategy_id, pair, time DESC);
