package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotRegistered 表示平台未注册该 code（无法确认已发布）。
var ErrNotRegistered = errors.New("algorithm not registered")

// StatusQuerier 查询算法最新版本状态（平台即真相源）。
// 合并进 awesomeProject 后，由同进程的 PostgreSQL 直接查询，无需跨进程 HTTP。
type StatusQuerier interface {
	LatestVersionStatus(ctx context.Context, code string) (string, error)
}

// SQLStatusQuerier 基于 PostgreSQL 直接查询最新版本状态。
// 取该 code 对应算法的最新一条 version 的 status（created_at DESC LIMIT 1）。
// 返回空串表示「有算法但尚无版本」，按 fail-closed 视为未发布。
type SQLStatusQuerier struct {
	DB *sql.DB
}

func (q *SQLStatusQuerier) LatestVersionStatus(ctx context.Context, code string) (string, error) {
	var status string
	err := q.DB.QueryRowContext(ctx, `
		SELECT av.status
		FROM algorithm_versions av
		JOIN algorithms a ON av.algorithm_id = a.id
		WHERE a.code = $1
		ORDER BY av.created_at DESC
		LIMIT 1`, code).Scan(&status)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("%w: code=%s", ErrNotRegistered, code)
	}
	if err != nil {
		return "", err
	}
	return status, nil
}
