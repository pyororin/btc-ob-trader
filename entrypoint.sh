#!/bin/sh

# Path to the config file that the bot needs
CONFIG_FILE="/data/params/trade_config.yaml"

echo "Waiting for config file to be created at ${CONFIG_FILE}..."

# Loop until the file exists
while [ ! -f "${CONFIG_FILE}" ]; do
  echo "Config file not found. Waiting 5 seconds..."
  sleep 5
done

echo "Config file found. Starting bot..."

# Execute the original entrypoint command
exec /usr/local/bin/obi-scalp-bot "$@"
