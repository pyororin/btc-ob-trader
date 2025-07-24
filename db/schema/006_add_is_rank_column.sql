DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_attribute WHERE attrelid = 'optimization_history'::regclass AND attname = 'is_rank' AND NOT attisdropped) THEN
        ALTER TABLE optimization_history ADD COLUMN is_rank INT;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_attribute WHERE attrelid = 'optimization_history'::regclass AND attname = 'retries_attempted' AND NOT attisdropped) THEN
        ALTER TABLE optimization_history ADD COLUMN retries_attempted INT;
    END IF;
END $$;
