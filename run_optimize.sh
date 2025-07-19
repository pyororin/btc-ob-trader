#!/bin/bash

# --- デフォルト値 ---
N_TRIALS=5000
HOURS_BEFORE=12
OVERRIDE=true
INTERVAL=360  # minutes

# --- 引数で上書き ---
if [ -n "$1" ]; then N_TRIALS="$1"; fi
if [ -n "$2" ]; then HOURS_BEFORE="$2"; fi
if [ -n "$3" ]; then OVERRIDE="$3"; fi
if [ -n "$4" ]; then INTERVAL="$4"; fi

# --- 秒に変換 ---
INTERVAL_SEC=$((INTERVAL * 60))

while true; do
    echo "$(date '+%Y-%m-%d %H:%M:%S') : 実行開始"
    echo "  N_TRIALS     : $N_TRIALS"
    echo "  HOURS_BEFORE : $HOURS_BEFORE"
    echo "  OVERRIDE     : $OVERRIDE"
    echo "  INTERVAL(min): $INTERVAL"

    # --- make optimize 実行 ---
    make optimize N_TRIALS="$N_TRIALS" HOURS_BEFORE="$HOURS_BEFORE" OVERRIDE="$OVERRIDE"

    echo "$(date '+%Y-%m-%d %H:%M:%S') : 実行完了"
    echo "$INTERVAL 分後に再実行します..."
    
    sleep "$INTERVAL_SEC"
done
