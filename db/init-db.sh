#!/bin/bash
set -e

# Wait for the database to be ready
until pg_isready -h ${POSTGRES_HOST:-localhost} -p 5432 -U "$POSTGRES_USER"
do
  echo "Waiting for postgres..."
  sleep 1
done

# Allow all connections
PGDATA=/var/lib/postgresql/data
echo "listen_addresses = '*'" >> "$PGDATA/postgresql.conf"
echo "host all all 0.0.0.0/0 md5" >> "$PGDATA/pg_hba.conf"

# Reload the configuration
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -c "SELECT pg_reload_conf();"

# Apply all .sql files in order
find /docker-entrypoint-initdb.d/ -type f -name "*.sql" | sort | while read f; do
  echo "Applying schema $f..."
  psql -v ON_ERROR_STOP=1 --host "${POSTGRES_HOST:-localhost}" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f "$f"
done
