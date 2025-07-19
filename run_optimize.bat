@echo off
setlocal

rem --- 引数のデフォルト値を設定 ---
set N_TRIALS=5000
set HOURS_BEFORE=6
set OVERRIDE=true
set INTERVAL=360

rem --- 引数を取得 ---
if not "%~1"=="" set N_TRIALS=%~1
if not "%~2"=="" set HOURS_BEFORE=%~2
if not "%~3"=="" set OVERRIDE=%~3
if not "%~4"=="" set INTERVAL=%~4

rem --- 実行間隔を秒に変換 ---
set /a INTERVAL_SEC=INTERVAL * 60

:LOOP
echo %date% %time% : 実行開始
echo   N_TRIALS     : %N_TRIALS%
echo   HOURS_BEFORE : %HOURS_BEFORE%
echo   OVERRIDE     : %OVERRIDE%
echo   INTERVAL(min): %INTERVAL%

rem --- make optimize を実行 ---
make optimize N_TRIALS=%N_TRIALS% HOURS_BEFORE=%HOURS_BEFORE% OVERRIDE=%OVERRIDE%

echo %date% %time% : 実行完了

echo %INTERVAL% 分後に再実行します...
timeout /t %INTERVAL_SEC% /nobreak

goto LOOP

endlocal
