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
