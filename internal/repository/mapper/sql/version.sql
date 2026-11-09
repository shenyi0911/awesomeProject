-- version.sql 版本 / 版本文件 / 变更日志 / 运行时解析 SQL（MyBatis mapper 风格）
-- 动态过滤采用「空串即忽略」惯例：($N = '' OR ...)，参数恒传、占位符索引固定。

-- name: VersionCreate
INSERT INTO algorithm_versions
	(id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)

-- name: VersionUpdate
UPDATE algorithm_versions SET
	description=$2, status=$3, is_active=$4, entry_point=$5, parameter_spec=$6, release_note=$7, updated_at=$8
WHERE id=$1

-- name: VersionDelete
DELETE FROM algorithm_versions WHERE id=$1

-- name: VersionGetByID
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at
FROM algorithm_versions WHERE id=$1

-- name: VersionCountByAlgorithm
SELECT COUNT(*) FROM algorithm_versions
WHERE algorithm_id = $1
  AND ($2 = '' OR version ILIKE '%' || $2 || '%')
  AND ($3 = '' OR status = $3)

-- name: VersionListByAlgorithm
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at
FROM algorithm_versions
WHERE algorithm_id = $1
  AND ($2 = '' OR version ILIKE '%' || $2 || '%')
  AND ($3 = '' OR status = $3)
ORDER BY created_at DESC
LIMIT $4 OFFSET $5

-- name: VersionGetActiveByAlgorithm
SELECT id, algorithm_id, version, description, status, is_active, entry_point, parameter_spec, release_note, created_at, updated_at
FROM algorithm_versions WHERE algorithm_id=$1 AND is_active=true LIMIT 1

-- name: VersionClearActive
UPDATE algorithm_versions SET is_active=false, updated_at=$1 WHERE algorithm_id=$2

-- name: VersionSetActive
UPDATE algorithm_versions SET is_active=true, status=$1, updated_at=$2 WHERE id=$3 AND algorithm_id=$4

-- name: VersionFileLoadByVersionID
SELECT id, version_id, algorithm_id, name, path, size, type, content_type
FROM version_files WHERE version_id=$1 ORDER BY created_at ASC

-- name: VersionFileDeleteByVersionID
DELETE FROM version_files WHERE version_id=$1

-- name: VersionFileInsert
INSERT INTO version_files (id, version_id, algorithm_id, name, path, size, type, content_type, created_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)

-- name: VersionFileListRefs
SELECT name, path FROM version_files WHERE version_id=$1 ORDER BY created_at ASC

-- name: RuntimeLatestReleasedVersion
SELECT v.id, v.version, v.status
FROM algorithm_versions v
JOIN algorithms a ON a.id = v.algorithm_id
WHERE a.code = $1
ORDER BY v.created_at DESC
LIMIT 1

-- name: RuntimeListReleasedCodes
WITH latest AS (
	SELECT DISTINCT ON (algorithm_id) id, algorithm_id, status
	FROM algorithm_versions
	ORDER BY algorithm_id, created_at DESC
)
SELECT a.code
FROM latest l
JOIN algorithms a ON a.id = l.algorithm_id
WHERE l.status = 'released'

-- name: ChangeLogCreate
INSERT INTO version_change_logs
	(id, algorithm_id, version_id, version, operator, action, change_time, change_desc, file_md5)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)

-- name: ChangeLogCountByVersionID
SELECT COUNT(*) FROM version_change_logs WHERE version_id=$1

-- name: ChangeLogListByVersionID
SELECT id, algorithm_id, version_id, version, operator, action, change_time, change_desc, file_md5
FROM version_change_logs WHERE version_id=$1 ORDER BY change_time DESC LIMIT $2 OFFSET $3

-- name: ChangeLogDeleteByVersionID
DELETE FROM version_change_logs WHERE version_id=$1
