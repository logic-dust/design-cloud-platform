@echo off
setlocal
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0install-win-agent-configless.ps1" %*
exit /b %ERRORLEVEL%
