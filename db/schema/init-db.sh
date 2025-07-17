#!/bin/bash
set -e

# Wait for the database to be ready
until pg_isready -h $POSTGRES_HOST -p 5432 -U $POSTGRES_USER
do
  echo "Waiting for postgres..."
  sleep 1
done

# Apply all .sql files in the directory
for f in /docker-entrypoint-initdb.d/*.sql; do
  echo "Applying schema $f..."
  psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -f "$f"
done
