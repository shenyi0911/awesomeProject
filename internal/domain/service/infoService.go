package service

import "awesomeProject/internal/domain/do/entity"

// InfoService 信息查询服务接口
type InfoService interface {
	GetAlgorithmInfo(algorithmID string) (*entity.AlgorithmInfo, error)
	GetDashboardStats() (entity.DashboardStats, error)
}
