-- schema.sql 建表 DDL（幂等，可重复执行）。启动时由 migrateSchema 读取，按分号切分逐条执行。
-- 四张表对应关系：
--   - algorithms            算法基本信息
--   - algorithm_versions    版本（文件/变更记录拆出独立表）
--   - version_files         版本文件元信息
--   - version_change_logs   操作/变更记录
-- 外键 ON DELETE CASCADE：删算法→删其全部版本→级联删文件与变更记录；删版本→级联删文件与变更记录。
-- 注意：CREATE TABLE IF NOT EXISTS 不会修改已存在的表，新增外键/索引只对新建库生效。

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
	revision       BIGINT       NOT NULL DEFAULT 0,
	created_at     TIMESTAMPTZ  NOT NULL,
	updated_at     TIMESTAMPTZ  NOT NULL,
	UNIQUE (algorithm_id, version)
);

CREATE INDEX IF NOT EXISTS idx_versions_algorithm ON algorithm_versions(algorithm_id);
CREATE INDEX IF NOT EXISTS idx_versions_active ON algorithm_versions(algorithm_id) WHERE is_active = TRUE;
-- 业务约束下沉到数据库：同一算法最多一个活跃版本、最多一个草稿、同版本内文件名唯一。
CREATE UNIQUE INDEX IF NOT EXISTS uq_versions_one_active ON algorithm_versions(algorithm_id) WHERE is_active = TRUE;
CREATE UNIQUE INDEX IF NOT EXISTS uq_versions_one_draft ON algorithm_versions(algorithm_id) WHERE status = 'draft';

CREATE TABLE IF NOT EXISTS version_files (
	id           VARCHAR(64)  PRIMARY KEY,
	version_id   VARCHAR(64)  NOT NULL REFERENCES algorithm_versions(id) ON DELETE CASCADE,
	algorithm_id VARCHAR(64)  NOT NULL,
	name         VARCHAR(255) NOT NULL,
	path         TEXT         NOT NULL,
	size         BIGINT       NOT NULL DEFAULT 0,
	type         VARCHAR(64),
	content_type VARCHAR(128),
	sort_order   INT          NOT NULL DEFAULT 0,
	created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_files_version ON version_files(version_id);
-- 先收敛历史重复的同名文件（保留最新一条），再建立唯一约束，避免旧库升级失败。
DELETE FROM version_files a
USING version_files b
WHERE a.version_id = b.version_id
  AND a.name = b.name
  AND (a.created_at, a.id) < (b.created_at, b.id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_version_files_name ON version_files(version_id, name);

CREATE TABLE IF NOT EXISTS version_change_logs (
	id           VARCHAR(64)  PRIMARY KEY,
	algorithm_id VARCHAR(64)  NOT NULL,
	version_id   VARCHAR(64)  NOT NULL REFERENCES algorithm_versions(id) ON DELETE CASCADE,
	version      VARCHAR(64)  NOT NULL,
	operator     VARCHAR(128),
	action       VARCHAR(32),
	change_time  TIMESTAMPTZ  NOT NULL,
	change_desc  TEXT,
	file_md5     VARCHAR(64)
);

CREATE INDEX IF NOT EXISTS idx_changelog_version ON version_change_logs(version_id);
CREATE INDEX IF NOT EXISTS idx_changelog_time ON version_change_logs(change_time);

-- 会话表：有状态算法的状态关联（首次调用 mint，后续调用携带 sessionId 复用）。
CREATE TABLE IF NOT EXISTS algorithm_sessions (
	session_id     VARCHAR(64)  PRIMARY KEY,
	algorithm_code VARCHAR(64)  NOT NULL,
	status         VARCHAR(32)  NOT NULL DEFAULT 'active',
	request_count  INT          NOT NULL DEFAULT 0,
	created_at     TIMESTAMPTZ  NOT NULL,
	expires_at     TIMESTAMPTZ  NOT NULL,
	last_active_at TIMESTAMPTZ  NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_code ON algorithm_sessions(algorithm_code);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON algorithm_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_sessions_active ON algorithm_sessions(algorithm_code, status, expires_at);

-- 增量迁移：旧库已在运行、无对应列时，幂等追加（仅兜底，新建表流程上面已含这些列）。
ALTER TABLE algorithms ADD COLUMN IF NOT EXISTS port INT NOT NULL DEFAULT 0;
ALTER TABLE algorithm_versions ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE version_files ADD COLUMN IF NOT EXISTS sort_order INT NOT NULL DEFAULT 0;
