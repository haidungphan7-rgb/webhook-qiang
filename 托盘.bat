@echo off
REM Starts the server in the background and shows the tray icon.
setlocal
pushd "%~dp0"

where pwsh >nul 2>nul
if %ERRORLEVEL%==0 ( set "PS=pwsh" ) else ( set "PS=powershell" )

start "" %PS% -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "%~dp0scripts\tray.ps1"
popd