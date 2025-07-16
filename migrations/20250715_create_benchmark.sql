-- benchmark_values テーブル
-- 特定の戦略のパフォーマンスを比較するための基準値を格納します。
-- 例えば、「単純保有」戦略の価値などを時系列で記録します。
CREATE TABLE IF NOT EXISTS benchmark_values (
    time TIMESTAMPTZ NOT NULL,
    strategy_id TEXT NOT NULL, -- ベンチマーク戦略の識別子 (例: 'buy_and_hold')
    value DECIMAL NOT NULL,    -- その時点でのベンチマーク価値
    PRIMARY KEY (time, strategy_id)
);

-- benchmark_values テーブルをHypertableに変換
SELECT create_hypertable('benchmark_values', 'time', if_not_exists => TRUE);

-- インデックスの追加
CREATE INDEX IF NOT EXISTS idx_benchmark_values_strategy_id_time ON benchmark_values (strategy_id, time DESC);
