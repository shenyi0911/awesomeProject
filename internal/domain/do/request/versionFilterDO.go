package request

import "awesomeProject/internal/domain/enum"

// VersionFilter 版本查询过滤条件
type VersionFilter struct {
	Version string
	Status  enum.VersionStatus
	Limit   int
	Offset  int
}
