package service

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/runtime"
)

// AlgorithmService 算法注册服务接口
type AlgorithmService interface {
	Register(ctx context.Context, req request.RegisterAlgorithmRequest) (*entity.Algorithm, error)
	Update(id string, req request.UpdateAlgorithmRequest) (*entity.Algorithm, error)
	Delete(id string) error
	GetByID(id string) (*entity.Algorithm, error)
	GetByCode(code string) (*entity.Algorithm, error)
	List(req request.ListAlgorithmRequest) ([]entity.Algorithm, int)
	// GetContract 返回该算法当前已发布版本的结构化方法契约（methods 段），供网关校验与前端调试页。
	// 返回 nil 表示无结构化契约（旧算法未写 methods 段），调用方应回退到 /metadata 的方法名列表。
	GetContract(code string) ([]runtime.MethodSpec, error)
}
