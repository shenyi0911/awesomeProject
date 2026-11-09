package service

import "awesomeProject/internal/domain/do/entity"

// InfoRepository 信息查询仓储接口
type InfoRepository interface {
	GetAlgorithmInfo(algorithmID string) (*entity.AlgorithmInfo, error)
	GetStats() (entity.DashboardStats, error)
}
