package entity

import (
	"time"

	"awesomeProject/internal/domain/enum"
)

// AlgorithmVersion 算法版本实体
// 状态流转：draft(草稿) -> released(发布) -> archived(归档)
// 每个版本独立绑定一套算法实体文件（Files），版本变更时旧版本文件保留，支持回溯。
type AlgorithmVersion struct {
	ID            string             `json:"id"`
	AlgorithmID   string             `json:"algorithmId"`
	Version       string             `json:"version"`
	Description   string             `json:"description"`
	Status        enum.VersionStatus `json:"status"`
	IsActive      bool               `json:"isActive"`
	EntryPoint    string             `json:"entryPoint"`
	ParameterSpec string             `json:"parameterSpec"`
	ReleaseNote   string             `json:"releaseNote"`
	Files         []AlgorithmFile    `json:"files"`
	CreatedAt     time.Time          `json:"createdAt"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}
