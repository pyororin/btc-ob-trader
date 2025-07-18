#!/bin/bash
set -e

# Wait for the database to be ready
until pg_isready -h ${POSTGRES_HOST:-localhost} -p 5432 -U $POSTGRES_USER
do
  echo "Waiting for postgres..."
  sleep 1
done

# Apply all .sql files in order
# The find command looks for all files ending with .sql in the /docker-entrypoint-initdb.d/ directory and its subdirectories,
# sorts them, and then executes them using psql.
find /docker-entrypoint-initdb.d/ -type f -name "*.sql" | sort | while read f; do
  echo "Applying schema $f..."
  psql -v ON_ERROR_STOP=1 --host "${POSTGRES_HOST:-localhost}" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" -f "$f"
done
