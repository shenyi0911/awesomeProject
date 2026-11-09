# migrate-minio-to-rustfs-docker.ps1
# 用 Docker 把原 MinIO 数据盘 (C:\minio\data) 的 algorithm-files 桶复制到 RustFS(9000)。
# 复制式迁移（官方推荐路径）：RustFS 绝不原地挂载 MinIO 磁盘（标准 GA 二进制不含
# rio-v2，原地挂载是 Preview/不可靠，有清空风险）。原盘全程保留作备份。
#
# 关键坑（2026-11-05 实跑验证）：MinIO 镜像 tag 必须与写盘时的本地 minio.exe 版本
# 完全一致，否则报 "Unknown xl meta version 3"。本地 exe = RELEASE.2025-07-23，
# 故用 minio/minio:20250723。SWR 代理的 :latest 是旧版，不可用于读盘。
#
# 用法：powershell -ExecutionPolicy Bypass -File migrate-minio-to-rustfs-docker.ps1
$ErrorActionPreference = 'Stop'

# Docker Desktop 默认不进旧会话 PATH；补上（不影响已运行的容器）
$dp = "C:\Program Files\Docker\Docker\resources\bin"
if (Test-Path $dp) { $env:PATH = $env:PATH + ";$dp" }

$MINIO_TAG = 'minio/minio:20250723'   # 必须与本地 C:\minio\minio.exe 同版本
$MC_IMAGE  = 'minio/mc:latest'
$SRC_DATA  = 'C:\minio\data'          # 原 MinIO 数据盘（只读源，保留作备份）
$SRC_PORT  = 9002                     # 宿主机临时端口，错开 RustFS 的 9000
$RUSTFS_PORT = 9000
$AK = 'minioadmin'; $SK = 'minioadmin'
$BUCKET = 'algorithm-files'

Write-Host "[1/4] Start temporary MinIO source (version-matched) on host :$SRC_PORT ..."
docker rm -f minio-src 2>$null
docker run -d --name minio-src `
  -p ${SRC_PORT}:9000 -p 9003:9001 `
  -v ${SRC_DATA}:/data `
  -e MINIO_ROOT_USER=$AK -e MINIO_ROOT_PASSWORD=$SK `
  $MINIO_TAG server /data --address ":9000" --console-address ":9001"
Start-Sleep -Seconds 8

Write-Host "[2/4] Mirror $BUCKET: MinIO(:$SRC_PORT) -> RustFS(:$RUSTFS_PORT) ..."
docker run --rm --entrypoint sh $MC_IMAGE -c 'mc alias set src http://host.docker.internal:9002 minioadmin minioadmin && mc alias set dst http://host.docker.internal:9000 minioadmin minioadmin && mc mb --ignore-existing dst/algorithm-files && mc mirror --overwrite src/algorithm-files dst/algorithm-files'

Write-Host "[3/4] Verify object count on RustFS ..."
docker run --rm --entrypoint sh $MC_IMAGE -c 'mc alias set dst http://host.docker.internal:9000 minioadmin minioadmin && mc ls --recursive dst/algorithm-files | wc -l'

Write-Host "[4/4] Remove temporary minio-src container (data disk untouched) ..."
docker rm -f minio-src
Write-Host "Done. RustFS is now the live backend at http://localhost:$RUSTFS_PORT" -ForegroundColor Green
