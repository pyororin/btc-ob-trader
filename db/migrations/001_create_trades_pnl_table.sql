-- 001_create_trades_pnl_table.sql

CREATE TABLE IF NOT EXISTS trades_pnl (
    trade_id BIGINT PRIMARY KEY,
    pnl NUMERIC(20, 8) NOT NULL,
    cumulative_pnl NUMERIC(20, 8) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
    -- FOREIGN KEY (trade_id) REFERENCES trades(transaction_id) -- TimescaleDBでは無効
);

-- Add cumulative columns to pnl_reports if they do not exist
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='cumulative_total_pnl') THEN
        ALTER TABLE pnl_reports ADD COLUMN cumulative_total_pnl NUMERIC(20, 8) NOT NULL DEFAULT 0;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='cumulative_winning_trades') THEN
        ALTER TABLE pnl_reports ADD COLUMN cumulative_winning_trades INTEGER NOT NULL DEFAULT 0;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='cumulative_losing_trades') THEN
        ALTER TABLE pnl_reports ADD COLUMN cumulative_losing_trades INTEGER NOT NULL DEFAULT 0;
    END IF;
END;
$$;
