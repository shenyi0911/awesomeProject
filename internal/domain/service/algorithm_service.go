package service

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/manifest"
)

// AlgorithmService is the algorithm registration service.
type AlgorithmService interface {
	Register(ctx context.Context, req request.RegisterAlgorithmRequest) (*entity.Algorithm, error)
	Update(ctx context.Context, id string, req request.UpdateAlgorithmRequest) (*entity.Algorithm, error)
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.Algorithm, error)
	GetByCode(ctx context.Context, code string) (*entity.Algorithm, error)
	List(ctx context.Context, req request.ListAlgorithmRequest) ([]entity.Algorithm, int, error)
	// GetContract returns the structured method contract (methods section) of the algorithm's released version.
	// It returns nil when no structured contract exists (older algorithms without a methods section);
	// callers should then fall back to the method names from /metadata.
	GetContract(ctx context.Context, code string) ([]manifest.MethodSpec, error)
}
