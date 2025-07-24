CREATE TABLE IF NOT EXISTS optimization_history (
    time TIMESTAMPTZ NOT NULL,
    trigger_type TEXT,
    is_hours FLOAT,
    oos_hours FLOAT,
    is_profit_factor FLOAT,
    is_sharpe_ratio FLOAT,
    oos_profit_factor FLOAT,
    oos_sharpe_ratio FLOAT,
    validation_passed BOOLEAN,
    best_params JSONB,
    is_rank INT,
    retries_attempted INT,
    PRIMARY KEY (time)
);

SELECT create_hypertable('optimization_history', 'time', if_not_exists => TRUE);
