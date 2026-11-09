# awesomeProject

算法库管理服务层骨架，基于 Go + Gin 实现（由原始 demo 改造而来）。

## 模块说明

| 模块 | 职责 | 主要接口 |
|------|------|----------|
| 算法注册 | 算法元数据的新增、修改、删除、查询 | `/api/v1/algorithms` |
| 版本管理 | 算法版本的创建、激活、查询 | `/api/v1/versions` |
| 信息查询 | 算法综合信息、搜索、统计 | `/api/v1/info` |

## 项目结构

```
awesomeProject/
├── main.go                   # 程序入口
├── internal/
│   ├── config/               # 配置加载
│   ├── domain/               # 领域模型 + 仓储接口（按聚合分包）
│   │   ├── algorithm/        # 算法聚合
│   │   │   ├── algorithm.go   # Algorithm 实体 / AlgorithmFile / AlgorithmFilter
│   │   │   ├── languageEnum.go # Language 枚举（Java/Python/C++/C#/Go）
│   │   │   └── repositoryInterface.go  # AlgorithmRepository 接口
│   │   ├── version/          # 版本聚合
│   │   │   ├── version.go     # AlgorithmVersion 实体 / VersionFilter
│   │   │   ├── statusEnum.go  # VersionStatus 枚举（draft/released/archived）
│   │   │   └── repositoryInterface.go  # VersionRepository 接口
│   │   └── info/             # 信息查询聚合
│   │       ├── info.go        # AlgorithmInfo / DashboardStats
│   │       └── repositoryInterface.go  # InfoRepository 接口
│   ├── handler/               # HTTP 处理器
│   ├── repository/           # 仓储实现（PostgreSQL 直连）
│   ├── router/               # 路由注册
│   └── service/              # 业务服务层
├── pkg/
│   ├── logger/               # 日志
│   ├── response/             # 统一响应封装
│   └── utils/                # 工具函数
├── config.yaml               # 配置文件
├── go.mod
└── README.md
```

## 快速启动

```bash
go mod tidy
go run .
```

服务默认监听 `http://localhost:8080`，健康检查 `/health`。

## 常用接口示例

### 注册算法
```bash
curl -X POST http://localhost:8080/api/v1/algorithms \
  -H "Content-Type: application/json" \
  -d '{
    "name": "SAG校正算法",
    "code": "sag_correction",
    "category": "survey",
    "description": "钻柱重力下凹校正",
    "author": "engineer"
  }'
```

### 创建版本
```bash
curl -X POST http://localhost:8080/api/v1/versions \
  -H "Content-Type: application/json" \
  -d '{
    "algorithmId": "<算法ID>",
    "version": "v1.0.0",
    "entryPoint": "sag.main",
    "parameterSpec": "{}"
  }'
```

### 发布版本（draft -> released，自动归档该算法旧版本）
```bash
curl -X POST "http://localhost:8080/api/v1/versions/<版本ID>/publish?operator=engineer"
```

### 归档版本（released/draft -> archived，保留历史）
```bash
curl -X POST "http://localhost:8080/api/v1/versions/<版本ID>/archive?operator=engineer"
```

### 查询算法信息
```bash
curl http://localhost:8080/api/v1/info/algorithms/<算法ID>
```

### 搜索算法
```bash
curl "http://localhost:8080/api/v1/info/search?keyword=sag&limit=10"
```

### 仪表盘统计
```bash
curl http://localhost:8080/api/v1/info/dashboard
```

## 后续扩展建议

1. **配置中心**：接入 Nacos / Consul / Kubernetes ConfigMap。
2. **日志链路**：增加 request_id 中间件，便于分布式链路追踪。
3. **参数校验**：引入 `github.com/go-playground/validator/v10` 做更复杂的字段校验。
4. **测试**：补充仓储层（PostgreSQL）与服务层的单元测试与集成测试。
