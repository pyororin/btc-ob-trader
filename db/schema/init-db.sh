#!/bin/bash
set -e

# Wait for the database to be ready
until pg_isready -h $POSTGRES_HOST -p 5432 -U $POSTGRES_USER
do
  echo "Waiting for postgres..."
  sleep 1
done

# Apply the schema
psql -h $POSTGRES_HOST -U $POSTGRES_USER -d $POSTGRES_DB -f /docker-entrypoint-initdb.d/001_tables.sql
