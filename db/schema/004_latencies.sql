-- 004_latencies.sql

-- 約定レイテンシを記録するテーブル
CREATE TABLE IF NOT EXISTS latencies (
    time TIMESTAMPTZ NOT NULL,
    order_id BIGINT NOT NULL,
    latency_ms BIGINT NOT NULL,
    PRIMARY KEY (time, order_id)
);

-- TimescaleDBのハイパーテーブルに変換
SELECT create_hypertable('latencies', 'time', if_not_exists => TRUE);

-- インデックスの作成
CREATE INDEX IF NOT EXISTS idx_latencies_order_id ON latencies (order_id);
CREATE INDEX IF NOT EXISTS idx_latencies_time_desc ON latencies (time DESC);
