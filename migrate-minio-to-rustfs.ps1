# migrate-minio-to-rustfs.ps1
# 一次性迁移脚本：把原 MinIO（C:\minio\data，端口 9000）的桶数据用 mc mirror 复制到 RustFS（C:\rustfs\data）。
# 采用「复制式」迁移（RustFS 官方当前推荐路径）：标准 RustFS 二进制不含 rio-v2，
# 原地挂载 MinIO 数据盘不可靠，复制式最安全。迁移后由 start-dev.ps1 启动 RustFS 占用 9000。
#
# 前置：
#   - 原 MinIO 仍在 9000 运行（C:\minio\minio.exe，minioadmin:minioadmin）
#   - 本脚本会按需下载 rustfs.exe / mc.exe 到 C:\rustfs\（需联网；失败则按提示手动下载）
# Run: powershell -ExecutionPolicy Bypass -File migrate-minio-to-rustfs.ps1

$ErrorActionPreference = "Stop"

$RUSTFS_BIN  = "C:\rustfs\rustfs.exe"
$RUSTFS_ZIP  = "C:\rustfs\rustfs.zip"
$MC_BIN      = "C:\rustfs\mc.exe"
$MINIO_DATA  = "C:\minio\data"
$RUSTFS_DATA = "C:\rustfs\data"
$TMP_PORT    = "9002"   # 迁移期间 RustFS 临时端口，避免与 MinIO 的 9000 冲突
$MINIO_PORT  = "9000"
$AK = "minioadmin"; $SK = "minioadmin"

# 0) 下载缺失的二进制（best-effort，需联网；失败则提示手动下载）
function Ensure-RustFS {
    if (Test-Path $RUSTFS_BIN) { Write-Host "[ok] RustFS 已存在: $RUSTFS_BIN"; return }
    $url = "https://github.com/rustfs/rustfs/releases/latest/download/rustfs-windows-x86_64-msvc-latest.zip"
    Write-Host "[*] 下载 RustFS (最新 release) ..."
    try {
        Invoke-WebRequest -Uri $url -OutFile $RUSTFS_ZIP -UseBasicParsing -TimeoutSec 180
        Expand-Archive -Path $RUSTFS_ZIP -DestinationPath "C:\rustfs" -Force
        $exe = Get-ChildItem "C:\rustfs" -Filter "rustfs.exe" -Recurse | Select-Object -First 1
        if (-not $exe) { throw "zip 中未找到 rustfs.exe" }
        Move-Item $exe.FullName $RUSTFS_BIN -Force
        Write-Host "    -> $RUSTFS_BIN"
    } catch {
        Write-Error "RustFS 自动下载失败: $_`n请手动从 https://github.com/rustfs/rustfs/releases 下载 windows 版，解压 rustfs.exe 到 $RUSTFS_BIN"
    }
}

function Ensure-MC {
    if (Test-Path $MC_BIN) { Write-Host "[ok] mc 已存在: $MC_BIN"; return }
    try {
        Invoke-WebRequest -Uri "https://dl.min.io/client/mc/release/windows-amd64/mc.exe" -OutFile $MC_BIN -UseBasicParsing -TimeoutSec 120
        Write-Host "    -> $MC_BIN"
    } catch {
        Write-Error "mc 自动下载失败: $_`n请手动下载到 $MC_BIN"
    }
}

Ensure-RustFS
Ensure-MC

# 1) 确认 MinIO 在 9000 运行（否则临时拉起以读取源数据）
$minioUp = netstat -ano 2>$null | Select-String ":$MINIO_PORT"
if (-not $minioUp) {
    Write-Host "[*] 未检测到 MinIO($MINIO_PORT)，尝试临时启动以读取源数据 ..."
    Start-Process -FilePath "C:\minio\minio.exe" -ArgumentList "server",$MINIO_DATA,"--address","127.0.0.1:$MINIO_PORT","--console-address","127.0.0.1:9001" -WindowStyle Normal
    Start-Sleep -Seconds 4
}

# 2) 启动 RustFS（临时端口 9002，全新数据目录）
New-Item -ItemType Directory -Force -Path $RUSTFS_DATA | Out-Null
$env:RUSTFS_ACCESS_KEY = $AK; $env:RUSTFS_SECRET_KEY = $SK
$env:RUSTFS_ADDRESS = ":$TMP_PORT"; $env:RUSTFS_CONSOLE_ADDRESS = ":9003"; $env:RUSTFS_CONSOLE_ENABLE = "true"
$rustfsProc = Start-Process -FilePath $RUSTFS_BIN -ArgumentList $RUSTFS_DATA -PassThru -WindowStyle Normal
Start-Sleep -Seconds 5

# 3) mc mirror 全桶复制
& $MC_BIN alias set minio  "http://127.0.0.1:$MINIO_PORT" $AK $SK
& $MC_BIN alias set rustfs "http://127.0.0.1:$TMP_PORT" $AK $SK
Write-Host "[*] 复制 MinIO -> RustFS（mc mirror）..."
& $MC_BIN mirror minio rustfs

# 4) 校验对象数量
Write-Host "[*] 校验对象数量 ..."
Write-Host "  MinIO :"; & $MC_BIN ls minio
Write-Host "  RustFS:"; & $MC_BIN ls rustfs

# 5) 收尾：停临时 RustFS 与 MinIO（之后由 start-dev.ps1 在 9000 拉起 RustFS）
Write-Host "[*] 停止临时 RustFS($TMP_PORT) 与 MinIO($MINIO_PORT) ..."
Stop-Process -Id $rustfsProc.Id -Force -ErrorAction SilentlyContinue
Get-Process -Name "minio" -ErrorAction SilentlyContinue | Stop-Process -Force

Write-Host ""
Write-Host "迁移完成。RustFS 数据已就绪于 $RUSTFS_DATA。"
Write-Host "接下来运行 start-dev.ps1 启动 RustFS（占用 9000，应用即连 RustFS）。"
Write-Host "确认无误后可删除旧 MinIO 数据 $MINIO_DATA 与 minio.exe 以释放空间。"
