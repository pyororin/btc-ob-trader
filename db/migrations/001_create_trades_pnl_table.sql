-- 001_create_trades_pnl_table.sql

CREATE TABLE trades_pnl (
    trade_id BIGINT PRIMARY KEY,
    pnl NUMERIC(20, 8) NOT NULL,
    cumulative_pnl NUMERIC(20, 8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- FOREIGN KEY (trade_id) REFERENCES trades(transaction_id) -- TimescaleDBでは無効
);

-- Add cumulative columns to pnl_reports
ALTER TABLE pnl_reports
ADD COLUMN IF NOT EXISTS cumulative_total_pnl NUMERIC(20, 8) NOT NULL DEFAULT 0,
ADD COLUMN IF NOT EXISTS cumulative_winning_trades INTEGER NOT NULL DEFAULT 0,
ADD COLUMN IF NOT EXISTS cumulative_losing_trades INTEGER NOT NULL DEFAULT 0;
