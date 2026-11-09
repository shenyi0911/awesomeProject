package request

import "awesomeProject/internal/domain/enum"

// ListVersionRequest 查询版本列表请求
type ListVersionRequest struct {
	AlgorithmID string             `form:"algorithmId" binding:"required"`
	Version     string             `form:"version"`
	Status      enum.VersionStatus `form:"status"`
	Limit       int                `form:"limit,default=20"`
	Offset      int                `form:"offset,default=0"`
}
