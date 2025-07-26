-- pnl_reports テーブルのカラムのデータ型を修正し、不足しているカラムを追加します。

DO $$
BEGIN
    -- profit_factor
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='profit_factor') THEN
        ALTER TABLE pnl_reports ALTER COLUMN profit_factor TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN profit_factor DOUBLE PRECISION;
    END IF;

    -- sharpe_ratio
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='sharpe_ratio') THEN
        ALTER TABLE pnl_reports ALTER COLUMN sharpe_ratio TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN sharpe_ratio DOUBLE PRECISION;
    END IF;

    -- sortino_ratio
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='sortino_ratio') THEN
        ALTER TABLE pnl_reports ALTER COLUMN sortino_ratio TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN sortino_ratio DOUBLE PRECISION;
    END IF;

    -- calmar_ratio
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='calmar_ratio') THEN
        ALTER TABLE pnl_reports ALTER COLUMN calmar_ratio TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN calmar_ratio DOUBLE PRECISION;
    END IF;

    -- max_drawdown
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='max_drawdown') THEN
        ALTER TABLE pnl_reports ALTER COLUMN max_drawdown TYPE DECIMAL;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN max_drawdown DECIMAL;
    END IF;

    -- recovery_factor
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='recovery_factor') THEN
        ALTER TABLE pnl_reports ALTER COLUMN recovery_factor TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN recovery_factor DOUBLE PRECISION;
    END IF;

    -- average_holding_period_seconds
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='average_holding_period_seconds') THEN
        ALTER TABLE pnl_reports ALTER COLUMN average_holding_period_seconds TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN average_holding_period_seconds DOUBLE PRECISION;
    END IF;

    -- average_winning_holding_period_seconds
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='average_winning_holding_period_seconds') THEN
        ALTER TABLE pnl_reports ALTER COLUMN average_winning_holding_period_seconds TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN average_winning_holding_period_seconds DOUBLE PRECISION;
    END IF;

    -- average_losing_holding_period_seconds
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='average_losing_holding_period_seconds') THEN
        ALTER TABLE pnl_reports ALTER COLUMN average_losing_holding_period_seconds TYPE DOUBLE PRECISION;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN average_losing_holding_period_seconds DOUBLE PRECISION;
    END IF;

    -- max_consecutive_wins
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='max_consecutive_wins') THEN
        ALTER TABLE pnl_reports ADD COLUMN max_consecutive_wins INT;
    END IF;

    -- max_consecutive_losses
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='max_consecutive_losses') THEN
        ALTER TABLE pnl_reports ADD COLUMN max_consecutive_losses INT;
    END IF;

    -- buy_and_hold_return
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='buy_and_hold_return') THEN
        ALTER TABLE pnl_reports ALTER COLUMN buy_and_hold_return TYPE DECIMAL;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN buy_and_hold_return DECIMAL;
    END IF;

    -- return_vs_buy_and_hold
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='return_vs_buy_and_hold') THEN
        ALTER TABLE pnl_reports ALTER COLUMN return_vs_buy_and_hold TYPE DECIMAL;
    ELSE
        ALTER TABLE pnl_reports ADD COLUMN return_vs_buy_and_hold DECIMAL;
    END IF;

    -- last_trade_id
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='pnl_reports' AND column_name='last_trade_id') THEN
        ALTER TABLE pnl_reports ADD COLUMN last_trade_id BIGINT;
    END IF;

END $$;
