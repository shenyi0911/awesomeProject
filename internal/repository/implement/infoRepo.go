package implement

import (
	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/repository/service"
)

// infoRepo 信息查询仓储
type infoRepo struct {
	algoRepo service.AlgorithmRepository
	verRepo  service.VersionRepository
}

// NewInfoRepository 创建信息查询仓储实例
func NewInfoRepository(algoRepo service.AlgorithmRepository, verRepo service.VersionRepository) service.InfoRepository {
	return &infoRepo{
		algoRepo: algoRepo,
		verRepo:  verRepo,
	}
}

func (r *infoRepo) GetAlgorithmInfo(algorithmID string) (*entity.AlgorithmInfo, error) {
	algo, err := r.algoRepo.GetByID(algorithmID)
	if err != nil {
		return nil, err
	}

	versions, _ := r.verRepo.ListByAlgorithm(algorithmID, request.VersionFilter{})

	stats := entity.AlgorithmStats{}
	for _, v := range versions {
		stats.TotalVersions++
		switch v.Status {
		case enum.VersionStatusDraft:
			stats.DraftVersions++
		case enum.VersionStatusReleased:
			stats.ReleasedVersions++
		}
	}

	return &entity.AlgorithmInfo{
		Algorithm: *algo,
		Versions:  versions,
		Stats:     stats,
	}, nil
}

func (r *infoRepo) GetStats() (entity.DashboardStats, error) {
	algos, _ := r.algoRepo.List(request.AlgorithmFilter{Limit: 10000})
	stats := entity.DashboardStats{
		TotalAlgorithms: len(algos),
		CategoryCounts:  make(map[string]int),
	}

	for _, algo := range algos {
		stats.CategoryCounts[algo.Category]++

		versions, _ := r.verRepo.ListByAlgorithm(algo.ID, request.VersionFilter{})
		for _, v := range versions {
			stats.TotalVersions++
			switch v.Status {
			case enum.VersionStatusDraft:
				stats.DraftVersions++
			case enum.VersionStatusReleased:
				stats.ReleasedVersions++
			}
		}
	}

	return stats, nil
}
