# awesomeProject

算法库管理 + 算法运行时编排服务（Go + Gin + PostgreSQL + S3 兼容对象存储 + Dapr）。

## 分层结构

```
awesomeProject/
├── main.go                        # 组装依赖、启动 HTTP 服务、优雅退出
├── config.yaml                    # 配置（敏感值用 ${ENV} 占位）
├── .env.example                   # 本地环境变量模板（复制为 .env）
├── internal/
│   ├── app/                       # 应用层编排：发布→拉起→回滚、调用→会话→转发
│   ├── config/                    # 配置加载（.env + ${VAR} 展开 + 默认值/校验）
│   ├── domain/
│   │   ├── do/entity|request/     # 领域实体与请求模型
│   │   ├── enum/                  # 枚举
│   │   ├── errs/                  # 带类别的领域错误（HTTP 映射依据）
│   │   ├── manifest/              # algorithm.yaml 启动契约模型与解析
│   │   ├── port/                  # 端口：仓储 / 文件存储 / 运行时接口
│   │   ├── service/               # 领域服务接口
│   │   └── implement/             # 领域服务实现（算法/版本/信息/会话）
│   ├── middleware/                # request-id、访问日志、CORS、体积限制、可选鉴权、panic 恢复
│   ├── handler/                   # HTTP 协议转换（薄层）
│   ├── repository/
│   │   ├── dao/                   # 连接与建表
│   │   ├── implement/             # PostgreSQL 仓储实现
│   │   └── mapper/sql/            # 内嵌 SQL（-- name: Xxx）
│   ├── runtime/                   # 算法运行时编排（dapr run、暂存、健康检查、自愈、发布闸门）
│   ├── filestorage/               # S3 兼容对象存储实现
│   └── router/                    # 路由注册
└── pkg/                           # logger / response / reqctx / utils
```

依赖方向：`handler → app → domain(service/port) ← repository/runtime/filestorage`。
领域层不反向依赖执行层或存储实现。

## 快速启动

```bash
cp .env.example .env     # 填入本地 PostgreSQL / S3 凭据（.env 已被 gitignore）
go mod tidy
go run .
```

服务默认监听 `http://localhost:8080`；`/health` 为存活检查，`/ready` 为就绪检查（PostgreSQL、对象存储、dapr 可执行文件与运行时 workspace）。
也可以使用 `start-dev.ps1`（会先拉起 PostgreSQL 与 RustFS 容器）。

## 配置

所有运行参数统一在 `config.yaml`（支持 `${VAR}` / `${VAR:-default}` 展开），敏感值从环境变量或 `.env` 注入：

- `PG_DSN`：PostgreSQL 连接串（必填）
- `S3_ACCESS_KEY` / `S3_SECRET_KEY`：对象存储凭据（必填）
- `API_TOKEN`：可选，非空时要求 `Authorization: Bearer <token>`
- `API_OPERATOR`：可选，已鉴权请求的审计操作人（`auth.operator`），优先于 query/form 中的 `operator`
- 其它：`SERVER_PORT`、`LOG_LEVEL`、`S3_ENDPOINT`、`S3_BUCKET`、`RUNTIME_WORKSPACE`、`DAPR_BIN`

限流与上传相关：

- `server.corsAllowOrigins`：跨域白名单，留空或 `["*"]` 允许所有来源
- `server.maxJsonBodyBytes` / `server.maxUploadBodyBytes`：请求体总大小上限（普通 JSON / multipart）
- `server.maxFileBytes`：单文件上限（默认 256 MiB）
- `server.maxMultipartMemoryBytes`：multipart 内存缓冲阈值（默认 8 MiB，超出写临时文件）
- `repository.sql.maxOpenConns` / `maxIdleConns` / `connMaxLifetime` / `connMaxIdleTime`：数据库连接池参数

## 主要接口

| 模块 | 接口 |
|------|------|
| 算法 | `POST/GET/PUT/DELETE /api/v1/algorithms...` |
| 版本 | `/api/v1/versions...`，`/publish`、`/archive`、`/files`、`/changelog` |
| 信息 | `/api/v1/info/algorithms/:id`、`/api/v1/info/dashboard` |
| 执行 | `POST /api/v1/{code}/{method}`、`GET /api/v1/{code}/metadata`、`GET /api/v1/{code}/contract` |
| 探针 | `GET /health`（存活）、`GET /ready`（数据库、对象存储、dapr、workspace） |
| 指标 | `GET /metrics`（Prometheus 文本格式，鉴权中间件放行） |

执行接口保持既有路径以兼容前端；调用前会做「最新版本必须为 released」的发布闸门校验。
有状态算法通过 `X-Session-Id` 复用会话，首次调用由平台 mint 并回写响应头/响应体。

## 可观测性（Prometheus + Grafana）

服务在 `/metrics` 暴露 Prometheus 文本指标，建议只在内网访问。

| 指标 | 说明 |
|------|------|
| `awesome_http_requests_total` | HTTP 请求数，按 `method/route/status` 维度 |
| `awesome_http_request_duration_seconds` | HTTP 接口耗时直方图，按 `method/route/status` 维度 |
| `awesome_invoke_phase_duration_seconds` | 算法调用分阶段耗时，按 `code/method/phase` 维度 |
| `awesome_invoke_total` | 算法调用结果计数，按 `code/method/status` 维度 |

`phase` 口径：

- `total`：平台一次调用总耗时
- `gate`：实例检查 + 发布闸门
- `meta`：算法元数据读取（带缓存）
- `session`：有状态算法会话解析
- `dapr_call`：平台 → Dapr → 算法 → 返回
- `algorithm`：Dapr → 本地计时代理 → 算法应用，表示算法自身执行耗时
- `postprocess`：会话注入、算法元数据回填
- `platform`：`total - dapr_call`，平台侧纯开销

平台开销 = `platform`；Dapr 开销 ≈ `dapr_call - algorithm`。`algorithm` 由平台在 Dapr 与算法之间插入的本地反向代理测量，因此无需额外抓取 daprd 指标。

重启后端后，启动算法时会自动使用新的“Dapr → 本地代理 → 算法”链路；之后的调用会同时产生平台、算法、Dapr 三段耗时。

本地启动 Prometheus + Grafana：

```bash
docker compose -f deploy/observability/docker-compose.yml up -d
```

- Prometheus：`http://localhost:9091`
- Grafana：`http://localhost:3000`（admin / admin），已自动配置 Prometheus 数据源和 “Awesome Algorithm Platform” 仪表盘
- 默认抓取 `host.docker.internal:8080`；平台不在本机时修改 `deploy/observability/prometheus.yml`

> 多个算法同时运行时，每个 daprd 需要独立的 `--metrics-port` 才能被 Prometheus 分别抓取；当前 Demo 默认不启用 daprd 指标。

## 关键行为

- **发布**：事务内归档同算法其它版本并置当前版本 released，再拉起算法进程；拉起失败自动回滚发布状态并恢复上一已发布版本。
- **升级**：已发布版本存在时可直接创建新草稿（同一时间只允许一个草稿），上传完成后再发布，避免先归档造成的停机。
- **归档**：逻辑删除；当算法已无已发布版本时回收进程。
- **删除**：删除算法时同步清理其会话记录与暂存目录；数据库层禁止删除仍有活跃已发布版本的算法。
- **运行时**：启动时按有限并发（4）拉起已发布算法并带指数退避重试；进程异常退出按 `runtime.maxRestarts` 自动重启（线性退避）；发布成功后清理旧版本暂存目录；算法进程只继承白名单环境变量，不会拿到数据库/对象存储/API 凭据；Linux 下按进程组回收整棵进程树。
- **文件**：按版本独立存储于 S3 兼容对象存储；上传路径会校验并禁止绝对路径与 `..`；文件集合修改使用 revision 乐观锁，避免并发覆盖；批量下载流式打包并净化 zip 条目名。
- **审计**：创建/编辑/发布/归档/文件变更写入 `version_change_logs`（只读操作不再写审计）。
