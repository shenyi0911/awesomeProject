package implement

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/port"
)

type infoService struct {
	infoRepo port.InfoRepository
}

// NewInfoService creates the information query service.
func NewInfoService(infoRepo port.InfoRepository) *infoService {
	return &infoService{infoRepo: infoRepo}
}

func (s *infoService) GetAlgorithmInfo(ctx context.Context, algorithmID string) (*entity.AlgorithmInfo, error) {
	return s.infoRepo.GetAlgorithmInfo(ctx, algorithmID)
}

func (s *infoService) GetDashboardStats(ctx context.Context) (entity.DashboardStats, error) {
	return s.infoRepo.GetStats(ctx)
}
