@echo off
REM Graphical launcher: starts the tray icon (which adopts or starts the server).
REM Written in GBK on purpose - cmd.exe reads .bat with the ANSI code page, so a UTF-8
REM file turns these comments into mojibake.
setlocal
pushd "%~dp0"

set "EXE=bin\webhook-zq.exe"
if not exist "%EXE%" set "EXE=webhook-zq.exe"

if not exist "%EXE%" (
  echo.
  echo 找不到程序，请先运行: pwsh ./scripts/setup.ps1
  pause
  exit /b 1
)

start "" "%EXE%" tray
set EXITCODE=%ERRORLEVEL%
popd

if not "%EXITCODE%"=="0" (
  echo.
  echo Exit code %EXITCODE%.
  pause
)