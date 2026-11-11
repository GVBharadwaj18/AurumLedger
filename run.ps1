# AurumLedger PowerShell Launcher with Automatic Policy Unblocking
Write-Host "==================================================================" -ForegroundColor Cyan
Write-Host "  Starting AurumLedger High-Concurrency Digital Gold Engine" -ForegroundColor Cyan
Write-Host "==================================================================" -ForegroundColor Cyan

# 1. Refresh PATH to find Go if newly installed
$env:Path = [System.Environment]::GetEnvironmentVariable("Path","Machine") + ";" + [System.Environment]::GetEnvironmentVariable("Path","User")
if (Test-Path "C:\Program Files\Go\bin") {
    $env:Path = "C:\Program Files\Go\bin;" + $env:Path
}

# 2. Unblock any executables and project files (removes Windows Mark-of-the-Web)
Write-Host "[1/3] Unblocking file permissions..." -ForegroundColor Yellow
Get-ChildItem -Path . -Filter *.exe | Unblock-File -ErrorAction SilentlyContinue

# 3. Compile binary if needed
if (-not (Test-Path "server.exe")) {
    Write-Host "[2/3] Compiling server.exe directly in workspace..." -ForegroundColor Yellow
    go build -mod=vendor -o server.exe ./cmd/server
} else {
    Write-Host "[2/3] Found pre-compiled server.exe" -ForegroundColor Green
}

# 4. Launch server directly
Write-Host "[3/3] Launching AurumLedger Server on port 8080..." -ForegroundColor Green
Write-Host "------------------------------------------------------------------" -ForegroundColor Cyan
Write-Host "👉 Open your browser to: http://localhost:8080/dashboard" -ForegroundColor Yellow
Write-Host "------------------------------------------------------------------" -ForegroundColor Cyan

try {
    .\server.exe
} catch {
    Write-Host ""
    Write-Host "⚠️ Execution was blocked by Windows policy." -ForegroundColor Red
    Write-Host "Solutions to run immediately:" -ForegroundColor Yellow
    Write-Host "  1. Right-click 'server.exe' -> Properties -> Check 'Unblock' at the bottom -> OK" -ForegroundColor White
    Write-Host "  2. In Windows Settings -> System -> For developers -> Turn 'Developer Mode' ON" -ForegroundColor White
    Write-Host "  3. Or run using Docker: docker compose up --build" -ForegroundColor White
}

