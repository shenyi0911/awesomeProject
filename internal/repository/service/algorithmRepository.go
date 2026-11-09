package service

import (
	"context"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// AlgorithmRepository 算法仓储接口
type AlgorithmRepository interface {
	Create(algo *entity.Algorithm) error
	Update(algo *entity.Algorithm) error
	Delete(id string) error
	GetByID(id string) (*entity.Algorithm, error)
	GetByCode(code string) (*entity.Algorithm, error)
	List(filter request.AlgorithmFilter) ([]entity.Algorithm, int)
	// ExistsByPort 判断是否存在「监听端口=port」的算法（排除 excludeID 自身），用于端口唯一性校验。
	ExistsByPort(port int, excludeID string) (bool, error)
	// PortForCode 按 code 返回平台指定的算法监听端口（运行时注入用）。
	PortForCode(ctx context.Context, code string) (int, error)
	// SetPort 由运行时在拉起时写回实际分配的端口（按主机动态分配），供前端回显。
	SetPort(ctx context.Context, code string, port int) error
}
