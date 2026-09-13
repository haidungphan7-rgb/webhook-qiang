@echo off
REM Graphical uninstaller: shows what is installed, you tick what to remove.
REM Written in GBK on purpose - cmd.exe reads .bat with the ANSI code page, so a UTF-8
REM file turns these comments into mojibake.
setlocal
pushd "%~dp0"

where pwsh >nul 2>nul
if %ERRORLEVEL%==0 ( set "PS=pwsh" ) else ( set "PS=powershell" )

%PS% -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\uninstaller.ps1"
set EXITCODE=%ERRORLEVEL%
popd

if not "%EXITCODE%"=="0" (
  echo.
  echo Uninstaller exited with code %EXITCODE%.
  pause
)