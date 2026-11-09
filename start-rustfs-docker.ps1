# start-rustfs-docker.ps1
# 用 Docker 跑 RustFS（S3 兼容对象存储），作为 awesomeProject 的文件存储后端。
# 镜像走华为云 SWR 国内镜像，规避 GitHub/Docker Hub 在国内被墙/慢的问题。
# 容器由 Docker 守护进程托管，常驻不随本脚本退出而终止；命名卷 rustfs-data 持久化数据。
#
# 前提：已安装并启动 Docker Desktop。
# 用法：powershell -ExecutionPolicy Bypass -File start-rustfs-docker.ps1

$ErrorActionPreference = 'Stop'

$RUSTFS_IMAGE = 'rustfs/rustfs:latest'
$SWR_MIRROR   = 'swr.cn-north-4.myhuaweicloud.com/ddn-k8s/docker.io/rustfs/rustfs:latest'

# 0) 确认 docker 可用
try {
    docker --version | Out-Null
} catch {
    Write-Host 'ERROR: 未检测到 docker。请先安装并启动 Docker Desktop。' -ForegroundColor Red
    exit 1
}

# 1) 拉取国内镜像并打 tag（已存在则跳过）
$existing = docker images --quiet $RUSTFS_IMAGE 2>$null
if (-not $existing) {
    Write-Host "[1/4] Pulling RustFS from Huawei SWR mirror ($SWR_MIRROR) ..."
    docker pull $SWR_MIRROR
    docker tag $SWR_MIRROR $RUSTFS_IMAGE
} else {
    Write-Host '[1/4] rustfs/rustfs:latest already present, skip pull.'
}

# 2) 同名容器已存在则先移除（保留命名卷 rustfs-data 中的数据）
if (docker ps -a --format '{{.Names}}' | Select-String '^rustfs$') {
    Write-Host '[2/4] Removing existing rustfs container (volume rustfs-data preserved) ...'
    docker rm -f rustfs | Out-Null
}

# 3) 启动 RustFS：命名卷持久化；端口 9000(S3 API)/9001(控制台)。
#    用 splatting 传参，避免 PowerShell 反引号续行 + 引号陷阱。
Write-Host '[3/4] Starting RustFS container on :9000 (console :9001) ...'
$dockerArgs = @(
    'run', '-d', '--name', 'rustfs', '--restart', 'unless-stopped',
    '-p', '9000:9000', '-p', '9001:9001',
    '-v', 'rustfs-data:/data',
    '-e', 'RUSTFS_ACCESS_KEY=minioadmin', '-e', 'RUSTFS_SECRET_KEY=minioadmin',
    '-e', 'RUSTFS_ADDRESS=:9000', '-e', 'RUSTFS_CONSOLE_ADDRESS=:9001',
    '-e', 'RUSTFS_CONSOLE_ENABLE=true', '-e', 'RUSTFS_OBS_LOGGER_LEVEL=error',
    '-e', 'RUSTFS_OBS_LOG_DIRECTORY=/var/log/rustfs/',
    $RUSTFS_IMAGE, '/usr/bin/rustfs', '/data'
)
docker @dockerArgs | Out-Null

# 4) 健康检查：探 S3 API 根路径（ListBuckets）。
#    说明：设了访问凭据后 /health 会返回 403（强制鉴权），不能据此判断生死；
#    只要 S3 根能返回 HTTP 响应（200/403 等，非连接失败）即代表服务在线。
Write-Host '[4/4] Waiting for S3 API health (http://localhost:9000/) ...'
$up = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $code = (curl.exe -s -o /dev/null -w '%{http_code}' http://localhost:9000/ 2>$null)
        if ($code -and $code -ne '000') { $up = $true; break }
    } catch {}
    Start-Sleep -Seconds 2
}
if ($up) {
    Write-Host 'RustFS is UP at http://localhost:9000  (console: http://localhost:9001)' -ForegroundColor Green
    Write-Host 'Now run in your project dir: go run .'
} else {
    Write-Host 'RustFS did not become healthy. Inspect logs: docker logs rustfs' -ForegroundColor Red
    exit 1
}
