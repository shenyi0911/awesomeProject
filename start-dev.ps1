# start-dev.ps1
# 一键开发启动器：PostgreSQL -> RustFS(Docker) -> Go 后端 -> Vue 前端。
# 说明：算法执行层由 Dapr 拉起（dapr run 挂 sidecar，网关经 /v1.0/invoke 转发）；
#       仓储直连 PostgreSQL；算法文件存 RustFS（S3 兼容，Docker 容器常驻，命名卷 rustfs-data 持久化）。
#       数据已从 MinIO 迁移至 RustFS（一次性 Docker 迁移已跑通，源盘 C:\minio\data 保留作备份）。
# 前提：已安装并启动 Docker Desktop（RustFS 走容器）、Go、Node。
# 用法：powershell -ExecutionPolicy Bypass -File start-dev.ps1

$ErrorActionPreference = 'Stop'

# 静默 TCP 探测：不用 Test-NetConnection（慢且刷进度条），直接连端口。
function Test-Port([int]$port) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $task = $client.ConnectAsync('127.0.0.1', $port)
        return ($task.Wait(400) -and $client.Connected)
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

function Wait-Port([int]$port, [int]$timeoutSec = 60) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        if (Test-Port $port) { return $true }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Assert-Command([string]$name) {
    $cmd = Get-Command $name -ErrorAction SilentlyContinue
    if (-not $cmd) { throw "未找到命令 '$name'，请确认已加入 PATH 后再运行本脚本。" }
    return $cmd.Source
}

# 只终止「本地端口处于 LISTENING」的进程，避免误伤远端端口相同的连接（PID 0 等）。
function Free-Port([int]$port) {
    Write-Host "[0] 释放端口 $port（应用）..." -ForegroundColor Cyan
    $lines = netstat -ano 2>$null | Select-String "^\s*TCP\s+\S+:$port\s+\S+\s+LISTENING\s+\d+\s*$"
    $seen = @{}
    foreach ($l in $lines) {
        if ($l.Line -match '(\d+)\s*$') {
            $p = $Matches[1]
            if ($p -ne '0' -and -not $seen.ContainsKey($p)) {
                $seen[$p] = $true
                Write-Host "  终止旧进程 PID $p"
                Stop-Process -Id $p -Force -ErrorAction SilentlyContinue
            }
        }
    }
    Start-Sleep -Seconds 1
}

# 0) 释放被占用的应用端口（8080），以便新后端绑定。
Free-Port 8080

# 1) PostgreSQL（端口 54329）。通常作为 Windows 服务已在运行；未监听则尝试启动服务。
Write-Host '[1/4] 检查 PostgreSQL (127.0.0.1:54329) ...' -ForegroundColor Cyan
if (-not (Wait-Port 54329 5)) {
    Write-Host '  PostgreSQL 未监听 54329，尝试启动服务 postgresql-x64-18 ...' -ForegroundColor Yellow
    try {
        Start-Service -Name 'postgresql-x64-18' -ErrorAction Stop
    } catch {
        Write-Host "  启动服务失败：$_" -ForegroundColor Red
    }
    if (-not (Wait-Port 54329 20)) {
        Write-Host '  PostgreSQL 仍不可用，后端将连不上数据库而退出。请检查 PostgreSQL 安装。' -ForegroundColor Red
    }
} else {
    Write-Host '  PostgreSQL 已就绪。' -ForegroundColor Green
}

# 2) RustFS 对象存储（端口 9000，S3 兼容）。走 Docker 容器（start-rustfs-docker.ps1），
#    数据由命名卷 rustfs-data 持久化；切勿用标准 RustFS 二进制原地挂载原 MinIO 盘（缺 rio-v2，会丢数据）。
Write-Host '[2/4] 启动 RustFS (Docker, :9000) ...' -ForegroundColor Cyan
& "$PSScriptRoot\start-rustfs-docker.ps1"

# 2.1) 闸门：确认 RustFS 确实起来了再继续（否则后端连不上对象存储，硬跑无意义）。
$rfsUp = $false
try {
    $code = (curl.exe -s -o /dev/null -w '%{http_code}' http://localhost:9000/ 2>$null)
    if ($code -and $code -ne '000') { $rfsUp = $true }
} catch {}
if (-not $rfsUp) {
    Write-Host '[2/4] RustFS 未能启动，终止后续步骤。请按上方提示处理 Docker 后重试。' -ForegroundColor Red
    exit 1
}
Write-Host '  RustFS 已就绪。' -ForegroundColor Green

# 3) 后端：Go 服务（端口 8080）。直接开窗口运行，Gin / Dapr 日志在窗口里实时可见。
Write-Host '[3/4] 启动后端 (Go, :8080) ...' -ForegroundColor Cyan
$goPath = Assert-Command 'go'
$beDir  = 'C:\Users\Lenovo\GolandProjects\awesomeProject'
Start-Process -FilePath $goPath -WorkingDirectory $beDir -ArgumentList 'run', '.' -WindowStyle Normal
if (Wait-Port 8080 90) {
    Write-Host '  后端已监听 8080。' -ForegroundColor Green
} else {
    Write-Host '  后端 90s 内未就绪，请看弹出的后端窗口里的报错（常见：编译失败 / 数据库连不上 / 8080 被占）。' -ForegroundColor Red
}

# 4) 前端：Vite（默认 5173，代理 /api -> 8080）。直接开窗口运行。
#    固定用本机真实 Node 的 npm（避免 PATH 先命中其它自带 node）。
Write-Host '[4/4] 启动前端 (Vite) ...' -ForegroundColor Cyan
$npmPath = 'C:\Program Files\nodejs\npm.cmd'
if (-not (Test-Path $npmPath)) { $npmPath = Assert-Command 'npm.cmd' }
$feDir = 'D:\awesome-web'
if (-not (Test-Path (Join-Path $feDir 'node_modules'))) {
    Write-Host '  node_modules 缺失，先 npm install（npmmirror）...' -ForegroundColor Yellow
    Start-Process -FilePath $npmPath -WorkingDirectory $feDir `
        -ArgumentList 'install', '--registry=https://registry.npmmirror.com' `
        -WindowStyle Normal -Wait
}
Start-Process -FilePath $npmPath -WorkingDirectory $feDir -ArgumentList 'run', 'dev' -WindowStyle Normal

# Vite 默认 5173，被占用则顺延到 5174/5175...；整体轮询最多 30s（npm 启动有延迟）。
$fePort = $null
$feDeadline = (Get-Date).AddSeconds(30)
while ((Get-Date) -lt $feDeadline) {
    foreach ($p in 5173..5183) {
        if (Test-Port $p) { $fePort = $p; break }
    }
    if ($fePort) { break }
    Start-Sleep -Milliseconds 500
}
if ($fePort) {
    Write-Host "  前端已监听 :$fePort -> http://localhost:$fePort/" -ForegroundColor Green
} else {
    Write-Host '  前端 30s 内未就绪，请看弹出的前端窗口里的报错。' -ForegroundColor Red
}

Write-Host ''
Write-Host '全部进程已启动。' -ForegroundColor Green
Write-Host '  后端：http://localhost:8080   （日志在后端窗口实时可见）'
if ($fePort) {
    Write-Host "  前端：http://localhost:$fePort/   （日志在前端窗口实时可见）"
} else {
    Write-Host '  前端：未就绪，请看弹出的前端窗口里的报错。'
}
Write-Host '  校验：打开前端页面，或浏览器访问 http://localhost:8080/health'
