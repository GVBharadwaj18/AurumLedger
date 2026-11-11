@echo off
setlocal
echo ==================================================================
echo   Starting AurumLedger High-Concurrency Digital Gold Engine
echo ==================================================================

rem Ensure Go is in PATH if installed in standard location
where go >nul 2>nul
if %errorlevel% neq 0 (
    set "PATH=C:\Program Files\Go\bin;%PATH%"
)

rem Unblock any file restrictions from Windows download
powershell -NoProfile -Command "Get-ChildItem -Path . -Filter *.exe | Unblock-File -ErrorAction SilentlyContinue" >nul 2>nul

rem Check if pre-compiled server.exe exists
if not exist "server.exe" (
    echo [1/2] Compiling AurumLedger binary...
    go build -mod=vendor -o server.exe ./cmd/server
    if %errorlevel% neq 0 (
        echo [ERROR] Failed to compile server.exe
        pause
        exit /b 1
    )
)

echo [2/2] Launching AurumLedger Server...
echo ------------------------------------------------------------------
echo Open your browser to: http://localhost:8080/dashboard
echo ------------------------------------------------------------------
server.exe
if %errorlevel% neq 0 (
    echo.
    echo [NOTE] If Windows Smart App Control blocked execution:
    echo   1. Right-click server.exe -^> Properties -^> Check 'Unblock' -^> Apply
    echo   2. Or enable Developer Mode in Windows Settings (System -^> For developers)
    echo   3. Or run with Docker: docker compose up --build
    pause
)

