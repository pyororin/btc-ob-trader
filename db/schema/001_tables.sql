-- TimescaleDB extensionの有効化 (既に有効な場合はスキップされる)
CREATE EXTENSION IF NOT EXISTS timescaledb CASCADE;

-- 板情報テーブル (order_book_updates)
-- L2オーダーブックの更新情報（スナップショットまたは差分）を格納
CREATE TABLE IF NOT EXISTS order_book_updates (
    time TIMESTAMPTZ NOT NULL,
    pair TEXT NOT NULL,
    side TEXT NOT NULL, -- 'bid' or 'ask'
    price DECIMAL NOT NULL,
    size DECIMAL NOT NULL,
    is_snapshot BOOLEAN NOT NULL DEFAULT FALSE, -- TRUEならスナップショット、FALSEなら差分更新
    CONSTRAINT check_side CHECK (side IN ('bid', 'ask'))
);

-- order_book_updates テーブルをHypertableに変換
-- time列を時間軸のパーティションキーとして使用
SELECT create_hypertable('order_book_updates', 'time', if_not_exists => TRUE);

-- order_book_updates テーブルの圧縮設定
-- 7日経過したチャンクを圧縮
ALTER TABLE order_book_updates SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'pair, side',
    timescaledb.compress_orderby = 'time DESC'
);
-- 圧縮ポリシーの追加 (例: 7日後に圧縮ジョブを実行)
SELECT add_compression_policy('order_book_updates', INTERVAL '7 days', if_not_exists => TRUE);


-- PnLサマリーテーブル (pnl_summary)
-- 定期的なPnLのスナップショットや重要なイベント発生時のPnLを記録
CREATE TABLE IF NOT EXISTS pnl_summary (
    time TIMESTAMPTZ NOT NULL,
    strategy_id TEXT NOT NULL DEFAULT 'default', -- 戦略識別子
    pair TEXT NOT NULL,                          -- 通貨ペア
    realized_pnl DECIMAL NOT NULL DEFAULT 0.0,   -- 実現損益
    unrealized_pnl DECIMAL NOT NULL DEFAULT 0.0, -- 未実現損益 (評価損益)
    total_pnl DECIMAL NOT NULL DEFAULT 0.0,      -- 合計損益
    position_size DECIMAL NOT NULL DEFAULT 0.0,  -- 現在のポジションサイズ (例: BTC)
    avg_entry_price DECIMAL NOT NULL DEFAULT 0.0 -- 平均取得価格
);

-- pnl_summary テーブルをHypertableに変換
SELECT create_hypertable('pnl_summary', 'time', if_not_exists => TRUE);

-- pnl_summary テーブルの圧縮設定
-- 30日経過したチャンクを圧縮
ALTER TABLE pnl_summary SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'strategy_id, pair',
    timescaledb.compress_orderby = 'time DESC'
);
-- 圧縮ポリシーの追加 (例: 30日後に圧縮ジョブを実行)
SELECT add_compression_policy('pnl_summary', INTERVAL '30 days', if_not_exists => TRUE);

-- インデックスの追加 (クエリパフォーマンス向上のため)
CREATE INDEX IF NOT EXISTS idx_order_book_updates_pair_time ON order_book_updates (pair, time DESC);
CREATE INDEX IF NOT EXISTS idx_order_book_updates_side_time ON order_book_updates (side, time DESC);

CREATE INDEX IF NOT EXISTS idx_pnl_summary_strategy_pair_time ON pnl_summary (strategy_id, pair, time DESC);

-- （オプション）より詳細なトレード履歴テーブル (trades)
-- Botが行った個々の取引を記録
-- CREATE TABLE IF NOT EXISTS trades (
--     time TIMESTAMPTZ NOT NULL,
--     trade_id TEXT PRIMARY KEY, -- 取引所が発行するIDまたは内部生成ID
--     client_order_id TEXT,      -- Botが発注時に設定するID
--     pair TEXT NOT NULL,
--     side TEXT NOT NULL,        -- 'buy' or 'sell'
--     price DECIMAL NOT NULL,
--     size DECIMAL NOT NULL,
--     fee DECIMAL,
--     role TEXT,                 -- 'maker' or 'taker'
--     CONSTRAINT check_trade_side CHECK (side IN ('buy', 'sell'))
-- );
-- SELECT create_hypertable('trades', 'time', if_not_exists => TRUE);
-- ALTER TABLE trades SET (timescaledb.compress, timescaledb.compress_segmentby = 'pair, side');
-- SELECT add_compression_policy('trades', INTERVAL '7 days', if_not_exists => TRUE);
-- CREATE INDEX IF NOT EXISTS idx_trades_pair_time ON trades (pair, time DESC);

-- （オプション）ポジション管理テーブル (positions)
-- 通貨ペアごとの現在の詳細なポジション情報を保持 (PnLサマリーと重複する部分もあるがより詳細)
-- CREATE TABLE IF NOT EXISTS positions (
--     last_updated_at TIMESTAMPTZ NOT NULL,
--     pair TEXT NOT NULL PRIMARY KEY,
--     size DECIMAL NOT NULL,
--     avg_entry_price DECIMAL NOT NULL,
--     unrealized_pnl_at_update DECIMAL -- このポジション情報が最後に更新された時点での未実現損益
-- );
-- このテーブルはHypertableにする必要はないかもしれないが、履歴を残したい場合はtime列を追加してHypertable化も可能

-- 注意:
-- - DECIMAL型は、精度とスケールを環境に合わせて調整してください。 (例: DECIMAL(16, 8))
-- - 圧縮設定 (compress_segmentby, compress_orderby) は、実際のクエリパターンやデータ特性に応じて調整すると、より効果的です。
-- - add_compression_policy の実行間隔も運用に合わせて調整してください。
-- - IF NOT EXISTS を使用しているため、スクリプトは複数回実行しても安全です。
-- - 本番環境でこれらのDDLを実行する前には、ステージング環境等で十分にテストしてください。
