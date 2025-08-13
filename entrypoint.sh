#!/bin/sh

echo "Starting bot..."

# Execute the original entrypoint command
exec /usr/local/bin/obi-scalp-bot "$@"
