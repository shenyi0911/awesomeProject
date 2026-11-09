# start-dev.ps1
# One-click dev launcher: PostgreSQL -> RustFS -> Go backend -> Vue frontend.
# 说明：算法执行层用 Dapr 拉起（dapr run 挂 sidecar，网关经 /v1.0/invoke 转发）；
#       仓储直连 PostgreSQL，算法文件存 RustFS（S3 兼容，由 MinIO 复制式迁移而来，运行时下载到本地暂存再启动）。
#       首次切换需先运行 migrate-minio-to-rustfs.ps1 把原 MinIO 桶数据复制到 C:\rustfs\data。
# Run: powershell -ExecutionPolicy Bypass -File start-dev.ps1

$ErrorActionPreference = "Continue"

# 0) Kill stale process holding the app port (8080) so the fresh binary can bind.
Write-Host "[0] Freeing port 8080 (app) ..."
$lines = netstat -ano 2>$null | Select-String ":(8080)\s"
$seen = @{}
foreach ($l in $lines) {
    if ($l -match '(\d+)\s*$') {
        $p = $Matches[1]
        if (-not $seen.ContainsKey($p)) {
            $seen[$p] = $true
            Write-Host "  killing stale PID $p"
            Stop-Process -Id $p -Force -ErrorAction SilentlyContinue
        }
    }
}
Start-Sleep -Seconds 1

# 1) PostgreSQL (port 54329). Usually already running as a Windows service.
Write-Host "[1/4] Checking PostgreSQL on 127.0.0.1:54329 ..."
$pgUp = netstat -ano 2>$null | Select-String ":54329"
if (-not $pgUp) {
    Write-Host "  PostgreSQL not detected, starting via pg_ctl ..."
    Start-Process -FilePath "C:\Program Files\PostgreSQL\18\bin\pg_ctl.exe" -ArgumentList "-D","C:\Program Files\PostgreSQL\18\data","-l","C:\Program Files\PostgreSQL\18\data\startup.log","start" -WindowStyle Hidden
} else {
    Write-Host "  PostgreSQL already running, skip."
}

# 2) RustFS 对象存储（端口 9000，S3 兼容，替代已归档的 MinIO）。
#    数据目录用独立的 C:\rustfs\data（首次需先从 MinIO 迁移，见 migrate-minio-to-rustfs.ps1）。
#    注意：标准 RustFS 二进制不含 rio-v2，切勿直接挂载原 MinIO 数据盘 C:\minio\data，
#          原地读取 MinIO 磁盘格式为 Preview 且不可靠，可能导致数据无法识别/丢失。
#    PowerShell Start-Process 会撞重复代理变量（http_proxy vs HTTP_PROXY），先清掉小写副本。
Write-Host "[2/4] Starting RustFS (port 9000) ..."
@('http_proxy','https_proxy','ftp_proxy','all_proxy') | ForEach-Object {
    try { Remove-Item -Path "Env:$_" -ErrorAction SilentlyContinue } catch {}
}
$rustfsUp = netstat -ano 2>$null | Select-String ":9000"
if (-not $rustfsUp) {
    if (-not (Test-Path "C:\rustfs\data")) {
        Write-Host "  WARN: C:\rustfs\data 不存在 —— 若这是首次切换，请先运行 migrate-minio-to-rustfs.ps1 从 MinIO 迁移数据。"
    }
    $env:RUSTFS_ACCESS_KEY = 'minioadmin'
    $env:RUSTFS_SECRET_KEY = 'minioadmin'
    $env:RUSTFS_ADDRESS = ':9000'
    $env:RUSTFS_CONSOLE_ADDRESS = ':9001'
    $env:RUSTFS_CONSOLE_ENABLE = 'true'
    Start-Process -FilePath "C:\rustfs\rustfs.exe" -ArgumentList "C:\rustfs\data" -WindowStyle Normal
    Start-Sleep -Seconds 3
    Write-Host "  RustFS launched (minioadmin:minioadmin). Console: http://127.0.0.1:9001"
} else {
    Write-Host "  RustFS already running on 9000, skip."
}

# 3) Backend: Go service (port 8080). 仓储直连 PostgreSQL；算法进程由运行时经 Dapr sidecar 拉起。
Write-Host "[3/4] Starting backend (Go, port 8080) ..."
Start-Process -FilePath "go" -WorkingDirectory "C:\Users\Lenovo\GolandProjects\awesomeProject" -ArgumentList "run","." -WindowStyle Normal

# 4) Frontend: Vite (default 5173, proxies /api to 8080)
Write-Host "[4/4] Starting frontend (Vite, http://localhost:5173) ..."
if (-not (Test-Path "D:\awesome-web\node_modules")) {
    Write-Host "  node_modules missing, running npm install (npmmirror) ..."
    Start-Process -FilePath "C:\Program Files\nodejs\npm.cmd" -WorkingDirectory "D:\awesome-web" -ArgumentList "install","--registry=https://registry.npmmirror.com" -WindowStyle Normal -Wait
}
Start-Process -FilePath "C:\Program Files\nodejs\npm.cmd" -WorkingDirectory "D:\awesome-web" -ArgumentList "run","dev" -WindowStyle Normal

Write-Host ""
Write-Host "All processes launched."
Write-Host "  Backend window : check Go logs for errors."
Write-Host "  Frontend window: open the printed URL (default http://localhost:5173)."
Write-Host "  Verify: open frontend, or run psql: SELECT * FROM algorithms;"
