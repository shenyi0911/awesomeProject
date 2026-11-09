package service

import (
	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// VersionRepository 版本仓储接口
type VersionRepository interface {
	Create(version *entity.AlgorithmVersion) error
	Update(version *entity.AlgorithmVersion) error
	Delete(id string) error
	GetByID(id string) (*entity.AlgorithmVersion, error)
	ListByAlgorithm(algorithmID string, filter request.VersionFilter) ([]entity.AlgorithmVersion, int)
	GetActiveByAlgorithm(algorithmID string) (*entity.AlgorithmVersion, error)
	SetActive(algorithmID string, versionID string) error
	// 变更记录
	CreateChangeLog(log *entity.VersionChangeLog) error
	ListChangeLog(versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error)
	// DeleteChangeLogs 删除某版本的全部变更记录（级联删除版本时调用）
	DeleteChangeLogs(versionID string) error
}
