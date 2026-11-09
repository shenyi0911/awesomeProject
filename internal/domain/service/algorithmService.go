package service

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// AlgorithmService 算法注册服务接口
type AlgorithmService interface {
	Register(ctx context.Context, req request.RegisterAlgorithmRequest) (*entity.Algorithm, error)
	Update(id string, req request.UpdateAlgorithmRequest) (*entity.Algorithm, error)
	Delete(id string) error
	GetByID(id string) (*entity.Algorithm, error)
	GetByCode(code string) (*entity.Algorithm, error)
	List(req request.ListAlgorithmRequest) ([]entity.Algorithm, int)
}
