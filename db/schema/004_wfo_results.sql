-- This table stores the results of each completed Walk-Forward Optimization (WFO) cycle.
-- Each row represents one IS/OOS cycle, allowing for robust analysis of strategy
-- performance over time and across different market regimes.

CREATE TABLE IF NOT EXISTS wfo_results (
    id SERIAL PRIMARY KEY,
    cycle_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,
    reason TEXT, -- To store the reason for failure

    -- Time windows for the cycle
    is_start_time TIMESTAMPTZ NOT NULL,
    is_end_time TIMESTAMPTZ NOT NULL,
    oos_end_time TIMESTAMPTZ NOT NULL,

    -- In-Sample (IS) metrics of the trial that was selected for OOS validation
    is_sharpe_ratio DOUBLE PRECISION,
    is_profit_factor DOUBLE PRECISION,
    is_max_drawdown DOUBLE PRECISION,
    is_relative_drawdown DOUBLE PRECISION,
    is_trades INTEGER,
    is_win_rate DOUBLE PRECISION,
    is_sqn DOUBLE PRECISION,

    -- Out-of-Sample (OOS) metrics from the validation run
    oos_sharpe_ratio DOUBLE PRECISION,
    oos_profit_factor DOUBLE PRECISION,
    oos_max_drawdown DOUBLE PRECISION,
    oos_trades INTEGER,
    oos_win_rate DOUBLE PRECISION,

    -- The best parameters found and their origin
    best_params JSONB,
    param_source VARCHAR(255),

    -- Timestamps
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Each cycle should be unique
    UNIQUE(cycle_id)
);

-- Create a trigger to automatically update the updated_at timestamp
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ language 'plpgsql';

DROP TRIGGER IF EXISTS update_wfo_results_updated_at ON wfo_results;
CREATE TRIGGER update_wfo_results_updated_at
    BEFORE UPDATE ON wfo_results
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Add comments to clarify the purpose of the table and key columns
COMMENT ON TABLE wfo_results IS 'Stores the results of each walk-forward optimization cycle.';
COMMENT ON COLUMN wfo_results.cycle_id IS 'Unique identifier for the WFO cycle, e.g., "cycle-2024-01".';
COMMENT ON COLUMN wfo_results.status IS 'Status of the cycle execution (e.g., "success", "failure").';
COMMENT ON COLUMN wfo_results.reason IS 'A text description of why a cycle failed, if applicable.';
COMMENT ON COLUMN wfo_results.is_sharpe_ratio IS 'Sharpe ratio from the In-Sample backtest of the best trial.';
COMMENT ON COLUMN wfo_results.oos_sharpe_ratio IS 'Sharpe ratio from the Out-of-Sample validation of the chosen parameters.';
COMMENT ON COLUMN wfo_results.best_params IS 'The best performing parameter set for the cycle (in JSON format).';
COMMENT ON COLUMN wfo_results.param_source IS 'The origin of the chosen parameters (e.g., "is_rank_1").';
