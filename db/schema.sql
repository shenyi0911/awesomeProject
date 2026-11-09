-- 算法库管理服务的多表关系结构 DDL（PostgreSQL）
-- 与 internal/repository/db.go 中的 migrateSchema 完全一致；
-- 也可手动执行本文件建表。四张表：
--   algorithms            算法基本信息
--   algorithm_versions    版本（状态：draft/released/archived/rollback）
--   version_files         版本文件元信息（文件字节仍存 MinIO，这里只存元数据）
--   version_change_logs   操作/变更记录
-- 外键 ON DELETE CASCADE 保证：删算法 -> 删其全部版本 -> 级联删文件与变更记录；删版本 -> 级联删文件与变更记录。

CREATE TABLE IF NOT EXISTS algorithms (
    id           VARCHAR(64)  PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    code         VARCHAR(64)  NOT NULL UNIQUE,
    description  TEXT,
    category     VARCHAR(128),
    author       VARCHAR(128),
    language     VARCHAR(32),
    scene        TEXT,
    stateful     BOOLEAN      NOT NULL DEFAULT FALSE,
    update_user  VARCHAR(128),
    port         INT          NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ  NOT NULL,
    updated_at   TIMESTAMPTZ  NOT NULL
);

CREATE TABLE IF NOT EXISTS algorithm_versions (
    id             VARCHAR(64)  PRIMARY KEY,
    algorithm_id   VARCHAR(64)  NOT NULL REFERENCES algorithms(id) ON DELETE CASCADE,
    version        VARCHAR(64)  NOT NULL,
    description    TEXT,
    status         VARCHAR(32)  NOT NULL,
    is_active      BOOLEAN      NOT NULL DEFAULT FALSE,
    entry_point    TEXT,
    parameter_spec TEXT,
    release_note   TEXT,
    created_at     TIMESTAMPTZ  NOT NULL,
    updated_at     TIMESTAMPTZ  NOT NULL,
    UNIQUE (algorithm_id, version)
);
CREATE INDEX IF NOT EXISTS idx_versions_algorithm ON algorithm_versions(algorithm_id);

CREATE TABLE IF NOT EXISTS version_files (
    id           VARCHAR(64)  PRIMARY KEY,
    version_id   VARCHAR(64)  NOT NULL REFERENCES algorithm_versions(id) ON DELETE CASCADE,
    algorithm_id VARCHAR(64)  NOT NULL,
    name         VARCHAR(255) NOT NULL,
    path         TEXT         NOT NULL,
    size         BIGINT       NOT NULL DEFAULT 0,
    type         VARCHAR(64),
    content_type VARCHAR(128),
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_files_version ON version_files(version_id);

CREATE TABLE IF NOT EXISTS version_change_logs (
    id           VARCHAR(64)  PRIMARY KEY,
    algorithm_id VARCHAR(64)  NOT NULL,
    version_id   VARCHAR(64)  NOT NULL,
    version      VARCHAR(64)  NOT NULL,
    operator     VARCHAR(128),
    action       VARCHAR(32),
    change_time  TIMESTAMPTZ  NOT NULL,
    change_desc  TEXT,
    file_md5     VARCHAR(64)
);
CREATE INDEX IF NOT EXISTS idx_changelog_version ON version_change_logs(version_id);
CREATE INDEX IF NOT EXISTS idx_changelog_time ON version_change_logs(change_time);
