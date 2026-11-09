package entity

import (
	"errors"
	"time"
)

// Session 算法调用会话实体
type Session struct {
	SessionID     string    // 会话唯一标识（平台生成，前缀 sess_）
	AlgorithmCode string    // 关联算法 code（会话只能被同一算法复用）
	Status        string    // active / closed（见 enum.StatusActive/StatusClosed）
	RequestCount  int       // 累计请求次数（仅成功复用的调用计入）
	CreatedAt     time.Time // 创建时间
	ExpiresAt     time.Time // 过期时间（每次复用时顺延 TTL）
	LastActiveAt  time.Time // 最近一次活跃时间
}

// DefaultTTL 会话无活动后的存活时长（默认 30 分钟）。
const DefaultTTL = 30 * time.Minute

// 会话相关错误（仓储层与调用方共用，便于按类型分支返回合适的 HTTP 状态码）。
var (
	ErrNotFound     = errors.New("session not found")
	ErrExpired      = errors.New("session expired")
	ErrCodeMismatch = errors.New("session code mismatch")
	ErrInactive     = errors.New("session inactive")
)
