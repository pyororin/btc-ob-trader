#!/bin/sh -e
echo "🌱  Streaming order_book_updates …"
DATA_ZIP="/seed/order_book_updates_jules.zip"

if [ ! -f "$DATA_ZIP" ]; then
  echo "⚠️  $DATA_ZIP not found; skipping seed." >&2
  exit 0
fi

TODAY=$(date -u "+%Y-%m-%d")

# DBスキーマ作成
psql -v ON_ERROR_STOP=1 --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" <<EOSQL
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE TABLE IF NOT EXISTS order_book_updates (
  time TIMESTAMPTZ NOT NULL,
  pair TEXT NOT NULL,
  side TEXT NOT NULL CHECK (side IN ('bid','ask')),
  price DECIMAL NOT NULL,
  size  DECIMAL NOT NULL,
  is_snapshot BOOLEAN NOT NULL DEFAULT FALSE
);
EOSQL

unzip -p "$DATA_ZIP" \
| psql --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" \
       -c "\COPY order_book_updates (time,pair,side,price,size,is_snapshot) FROM STDIN CSV HEADER"

echo "✅  Seed completed"
