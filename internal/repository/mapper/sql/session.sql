-- session.sql 会话仓储 SQL（MyBatis mapper 风格）

-- name: SessionCreate
INSERT INTO algorithm_sessions
	(session_id, algorithm_code, status, request_count, created_at, expires_at, last_active_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)

-- name: SessionGet
SELECT session_id, algorithm_code, status, request_count, created_at, expires_at, last_active_at
FROM algorithm_sessions WHERE session_id=$1

-- name: SessionTouch
UPDATE algorithm_sessions
SET last_active_at=$1, expires_at=$2, request_count=request_count+1
WHERE session_id=$3

-- name: SessionDelete
DELETE FROM algorithm_sessions WHERE session_id=$1

-- name: SessionDeleteExpired
DELETE FROM algorithm_sessions WHERE expires_at < $1
