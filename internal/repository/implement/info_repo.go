package implement

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/port"
)

// infoRepo implements information queries using SQL aggregation to avoid N+1 queries per algorithm or version.
type infoRepo struct {
	algoRepo port.AlgorithmRepository
	verRepo  port.VersionRepository
}

var _ port.InfoRepository = (*infoRepo)(nil)

// NewInfoRepository creates the information query repository.
func NewInfoRepository(algoRepo port.AlgorithmRepository, verRepo port.VersionRepository) *infoRepo {
	return &infoRepo{algoRepo: algoRepo, verRepo: verRepo}
}

func (r *infoRepo) GetAlgorithmInfo(ctx context.Context, algorithmID string) (*entity.AlgorithmInfo, error) {
	algo, err := r.algoRepo.GetByID(ctx, algorithmID)
	if err != nil {
		return nil, err
	}
	versions, err := r.verRepo.ListAllByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, err
	}
	counts, err := r.verRepo.CountStatusByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, err
	}
	return &entity.AlgorithmInfo{
		Algorithm: *algo,
		Versions:  versions,
		Stats:     statsFromCounts(counts),
	}, nil
}

func (r *infoRepo) GetStats(ctx context.Context) (entity.DashboardStats, error) {
	total, err := r.algoRepo.Count(ctx)
	if err != nil {
		return entity.DashboardStats{}, err
	}
	categories, err := r.algoRepo.CategoryCounts(ctx)
	if err != nil {
		return entity.DashboardStats{}, err
	}
	statuses, err := r.verRepo.GlobalStatusCounts(ctx)
	if err != nil {
		return entity.DashboardStats{}, err
	}
	stats := statsFromCounts(statuses)
	return entity.DashboardStats{
		TotalAlgorithms:  total,
		TotalVersions:    stats.TotalVersions,
		DraftVersions:    stats.DraftVersions,
		ReleasedVersions: stats.ReleasedVersions,
		CategoryCounts:   categories,
	}, nil
}

// statsFromCounts aggregates a status -> count map into version statistics.
func statsFromCounts(counts map[string]int) entity.AlgorithmStats {
	stats := entity.AlgorithmStats{}
	for status, n := range counts {
		stats.TotalVersions += n
		switch enum.VersionStatus(status) {
		case enum.VersionStatusDraft:
			stats.DraftVersions += n
		case enum.VersionStatusReleased:
			stats.ReleasedVersions += n
		}
	}
	return stats
}
