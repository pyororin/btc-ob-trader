-- benchmark_values テーブルを作成する
CREATE TABLE IF NOT EXISTS benchmark_values (
    time TIMESTAMPTZ NOT NULL,
    price DOUBLE PRECISION NOT NULL,
    PRIMARY KEY (time)
);

-- TimescaleDBのハイパーテーブルに変換する
-- timeを基準にチャンクを自動生成する
SELECT create_hypertable('benchmark_values', 'time', if_not_exists => TRUE);

-- インデックスを作成してクエリパフォーマンスを向上させる
CREATE INDEX IF NOT EXISTS idx_benchmark_values_time ON benchmark_values (time DESC);

-- 圧縮設定を明示的に定義する
-- 既存のポリシーを削除し、意図しない設定が残らないようにする
SELECT remove_compression_policy('benchmark_values', if_exists => TRUE);

-- 圧縮を有効化
ALTER TABLE benchmark_values SET (timescaledb.compress = true, timescaledb.compress_orderby = 'time DESC');

-- 7日後にチャンクを圧縮するポリシーを追加
SELECT add_compression_policy('benchmark_values', INTERVAL '7 days', if_not_exists => TRUE);
