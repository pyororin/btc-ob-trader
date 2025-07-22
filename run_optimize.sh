#!/bin/bash

# --- デフォルト値 ---
N_TRIALS=2500
HOURS_BEFORE=6
OVERRIDE=true
INTERVAL=60  # minutes

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
    START_TIME=$(date +%s)
    make optimize N_TRIALS="$N_TRIALS" HOURS_BEFORE="$HOURS_BEFORE" OVERRIDE="$OVERRIDE"
    END_TIME=$(date +%s)

    # --- 実行時間を計算 ---
    EXECUTION_TIME=$((END_TIME - START_TIME))

    # --- 待機時間を計算 ---
    WAIT_TIME=$((INTERVAL_SEC - EXECUTION_TIME))

    echo "$(date '+%Y-%m-%d %H:%M:%S') : 実行完了"
    
    if [ $WAIT_TIME -gt 0 ]; then
        WAIT_MINUTES=$((WAIT_TIME / 60))
        echo "make optimize の実行時間: $EXECUTION_TIME 秒"
        echo "$WAIT_MINUTES 分後に再実行します..."
        sleep "$WAIT_TIME"
    else
        echo "make optimize の実行時間が INTERVAL を超えたため、すぐに再実行します..."
    fi
done
