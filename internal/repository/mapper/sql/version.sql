-- version.sql 版本 / 版本文件 / 变更日志 / 运行时解析 SQL（MyBatis mapper 风格）
-- 动态过滤采用「空串即忽略」惯例：($N = '' OR ...)，参数恒传、占位符索引固定。
-- 分页上限只在 API 边界处理（service 层），仓储层不做截断，避免内部全量查询被静默截断。

-- name: VersionCreate
INSERT INTO algorithm_versions
	(id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at, revision)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)

-- name: VersionUpdateMeta
UPDATE algorithm_versions SET
	description=$2, entry_point=$3, parameter_spec=$4, release_note=$5, updated_at=$6
WHERE id=$1 AND status='draft'

-- name: VersionDelete
DELETE FROM algorithm_versions WHERE id=$1

-- name: VersionGetByID
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at, revision
FROM algorithm_versions WHERE id=$1

-- name: VersionCountByAlgorithm
SELECT COUNT(*) FROM algorithm_versions
WHERE algorithm_id = $1
  AND ($2 = '' OR version ILIKE '%' || $2 || '%')
  AND ($3 = '' OR status = $3)

-- name: VersionListByAlgorithm
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at, revision
FROM algorithm_versions
WHERE algorithm_id = $1
  AND ($2 = '' OR version ILIKE '%' || $2 || '%')
  AND ($3 = '' OR status = $3)
ORDER BY created_at DESC, id DESC
LIMIT $4 OFFSET $5

-- name: VersionListAllByAlgorithm
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at, revision
FROM algorithm_versions
WHERE algorithm_id = $1
ORDER BY created_at DESC, id DESC

-- name: VersionGetActiveByAlgorithm
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at, revision
FROM algorithm_versions WHERE algorithm_id=$1 AND is_active=true LIMIT 1

-- name: VersionLockAlgorithm
SELECT id FROM algorithms WHERE id=$1 FOR UPDATE

-- name: VersionArchiveOthers
UPDATE algorithm_versions
SET status='archived', is_active=false, revision=revision+1, updated_at=$2
WHERE algorithm_id=$1 AND id<>$3 AND status<>'archived'

-- name: VersionActivate
UPDATE algorithm_versions
SET status='released', is_active=true, parameter_spec=$2, revision=revision+1, updated_at=$3
WHERE id=$1 AND algorithm_id=$4

-- name: VersionSetArchived
UPDATE algorithm_versions
SET status='archived', is_active=false, revision=revision+1, updated_at=$2
WHERE id=$1

-- name: VersionSetDraft
UPDATE algorithm_versions
SET status='draft', is_active=false, revision=revision+1, updated_at=$2
WHERE id=$1

-- name: VersionRestoreActive
UPDATE algorithm_versions
SET status='released', is_active=true, revision=revision+1, updated_at=$2
WHERE id=$1

-- name: VersionBumpRevision
UPDATE algorithm_versions
SET revision=revision+1, updated_at=$2
WHERE id=$1 AND revision=$3

-- name: VersionStatusCountsByAlgorithm
SELECT status, COUNT(*) FROM algorithm_versions WHERE algorithm_id=$1 GROUP BY status

-- name: VersionGlobalStatusCounts
SELECT status, COUNT(*) FROM algorithm_versions GROUP BY status

-- name: VersionFileLoadByVersionID
SELECT id, version_id, algorithm_id, name, path, size, type, content_type
FROM version_files WHERE version_id=$1 ORDER BY sort_order ASC, id ASC

-- name: VersionFilesByVersionIDs
SELECT version_id, name, path, size, type, content_type
FROM version_files WHERE version_id = ANY($1) ORDER BY sort_order ASC, id ASC

-- name: VersionFileDeleteByVersionID
DELETE FROM version_files WHERE version_id=$1

-- name: VersionFileInsert
INSERT INTO version_files (id, version_id, algorithm_id, name, path, size, type, content_type, created_at, sort_order)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)

-- name: VersionFileListRefs
SELECT name, path FROM version_files WHERE version_id=$1 ORDER BY sort_order ASC, id ASC

-- name: RuntimeLatestReleasedVersion
SELECT v.id, v.version, v.status
FROM algorithm_versions v
JOIN algorithms a ON a.id = v.algorithm_id
WHERE a.code = $1 AND v.status = 'released' AND v.is_active = true
ORDER BY v.created_at DESC, v.id DESC
LIMIT 1

-- name: RuntimeListReleasedCodes
SELECT DISTINCT a.code
FROM algorithms a
JOIN algorithm_versions v ON v.algorithm_id = a.id
WHERE v.status = 'released' AND v.is_active = true
ORDER BY a.code

-- name: ChangeLogCreate
INSERT INTO version_change_logs
	(id, algorithm_id, version_id, version, operator, action, change_time, change_desc, file_md5)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)

-- name: ChangeLogCountByVersionID
SELECT COUNT(*) FROM version_change_logs WHERE version_id=$1

-- name: ChangeLogListByVersionID
SELECT id, algorithm_id, version_id, version, operator, action, change_time, change_desc, file_md5
FROM version_change_logs WHERE version_id=$1 ORDER BY change_time DESC, id DESC LIMIT $2 OFFSET $3

-- name: ChangeLogDeleteByVersionID
DELETE FROM version_change_logs WHERE version_id=$1
