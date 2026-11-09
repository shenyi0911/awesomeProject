package implement

import (
	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/service"
	repoiface "awesomeProject/internal/repository/service"
)

type infoService struct {
	infoRepo repoiface.InfoRepository
}

func NewInfoService(infoRepo repoiface.InfoRepository) service.InfoService {
	return &infoService{
		infoRepo: infoRepo,
	}
}

func (s *infoService) GetAlgorithmInfo(algorithmID string) (*entity.AlgorithmInfo, error) {
	return s.infoRepo.GetAlgorithmInfo(algorithmID)
}

func (s *infoService) GetDashboardStats() (entity.DashboardStats, error) {
	return s.infoRepo.GetStats()
}
