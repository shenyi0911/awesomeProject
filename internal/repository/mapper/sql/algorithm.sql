-- algorithm.sql 算法仓储 SQL（MyBatis mapper 风格）
-- 约定：每条语句以 `-- name: Xxx` 标注，代码中 SQL("Xxx") 引用（见 sqlMapper.go）。
-- 动态过滤统一采用「空串即忽略」惯例：($N = '' OR col ILIKE ...)——参数恒传、占位符索引固定，
-- 避免在 Go 端拼接 SQL 字符串；空串条件与「跳过该条件」语义等价。

-- name: AlgorithmCreate
INSERT INTO algorithms
	(id, name, code, description, category, author, language, scene, stateful, update_user, port, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)

-- name: AlgorithmUpdate
UPDATE algorithms SET
	name=$2, code=$3, description=$4, category=$5, author=$6,
	language=$7, scene=$8, stateful=$9, port=$10, update_user=$11, updated_at=$12
WHERE id=$1

-- name: AlgorithmDelete
DELETE FROM algorithms WHERE id=$1

-- name: AlgorithmGetByID
SELECT id, name, code, description, category, author, language, scene, stateful, update_user, port, created_at, updated_at
FROM algorithms WHERE id=$1

-- name: AlgorithmGetByCode
SELECT id, name, code, description, category, author, language, scene, stateful, update_user, port, created_at, updated_at
FROM algorithms WHERE code=$1

-- name: AlgorithmCountByFilter
SELECT COUNT(*) FROM algorithms
WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')
  AND ($2 = '' OR code ILIKE '%' || $2 || '%')

-- name: AlgorithmListByFilter
SELECT a.id, a.name, a.code, a.description, a.category, a.author, a.language, a.scene, a.stateful, a.update_user, a.port, a.created_at, a.updated_at,
		COALESCE(lv.version, ''), COALESCE(lv.status, '')
FROM algorithms a
LEFT JOIN LATERAL (
	SELECT version, status
	FROM algorithm_versions
	WHERE algorithm_id = a.id
	ORDER BY created_at DESC
	LIMIT 1
) lv ON true
WHERE ($1 = '' OR a.name ILIKE '%' || $1 || '%')
  AND ($2 = '' OR a.code ILIKE '%' || $2 || '%')
ORDER BY a.created_at DESC
LIMIT $3 OFFSET $4

-- name: AlgorithmExistsByPort
SELECT COUNT(*) FROM algorithms WHERE port=$1 AND ($2 = '' OR id <> $2)

-- name: AlgorithmPortForCode
SELECT port FROM algorithms WHERE code=$1

-- name: AlgorithmSetPort
UPDATE algorithms SET port=$1, updated_at=NOW() WHERE code=$2
