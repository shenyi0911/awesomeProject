package service

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
)

// InfoService is the information query service.
type InfoService interface {
	GetAlgorithmInfo(ctx context.Context, algorithmID string) (*entity.AlgorithmInfo, error)
	GetDashboardStats(ctx context.Context) (entity.DashboardStats, error)
}
