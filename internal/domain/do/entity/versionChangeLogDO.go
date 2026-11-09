package entity

import "time"

// VersionChangeLog 版本变更记录
// 记录版本号、操作人、变更时间、变更说明、文件 MD5（如有），用于审计与回溯。
type VersionChangeLog struct {
	ID          string    `json:"id"`
	AlgorithmID string    `json:"algorithmId"`
	VersionID   string    `json:"versionId"`
	Version     string    `json:"version"`
	Operator    string    `json:"operator"`
	Action      string    `json:"action"`
	ChangeTime  time.Time `json:"changeTime"`
	ChangeDesc  string    `json:"changeDesc"`
	FileMD5     string    `json:"fileMd5"` // 文件 MD5（仅文件相关动作有值）
}
