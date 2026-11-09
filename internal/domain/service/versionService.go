package service

import (
	"context"
	"io"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
)

// VersionService 版本管理服务接口
type VersionService interface {
	Create(req request.CreateVersionRequest) (*entity.AlgorithmVersion, error)
	Update(id string, req request.UpdateVersionRequest) (*entity.AlgorithmVersion, error)
	Delete(id string) error
	GetByID(id string) (*entity.AlgorithmVersion, error)
	// AlgorithmCode 由算法 ID 取算法 code（运行时据此定位算法包与 Dapr app-id）
	AlgorithmCode(algoID string) (string, error)
	List(req request.ListVersionRequest) ([]entity.AlgorithmVersion, int)
	GetActive(algorithmID string) (*entity.AlgorithmVersion, error)
	// 状态流转（设计：草稿 -> 发布 -> 归档，归档不可改、不可物理删除）
	Publish(id string, operator string) (*entity.AlgorithmVersion, error)
	Archive(id string, operator string) (*entity.AlgorithmVersion, error)
	// RevertPublish 将已发布版本回滚为草稿（拉起失败时调用，避免留下「已发布但没起来」的僵尸版本）
	RevertPublish(id string, operator string) error
	// 版本级文件（每个版本独立一套文件）
	UploadFile(ctx context.Context, versionID string, in request.UploadFileInput, operator string) (*entity.AlgorithmFile, error)
	UploadFiles(ctx context.Context, versionID string, ins []request.UploadFileInput, operator string) ([]*entity.AlgorithmFile, error)
	DeleteFile(ctx context.Context, versionID, key, operator string) error
	GetFileURL(ctx context.Context, versionID, key, operator string) (string, error)
	// BatchDownloadFiles 批量下载：将选中的版本文件从对象存储拉取后打包为 zip 返回（保留相对路径结构）
	BatchDownloadFiles(ctx context.Context, versionID string, keys []string, operator string) (io.ReadCloser, string, error)
	// 变更记录
	ListChangeLog(versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error)
}
