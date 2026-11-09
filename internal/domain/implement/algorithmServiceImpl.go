package implement

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/service"
	"awesomeProject/internal/filestorage"
	repoiface "awesomeProject/internal/repository/service"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

type algorithmService struct {
	repo    repoiface.AlgorithmRepository
	verSvc  service.VersionService
	storage filestorage.FileStorage
}

func NewAlgorithmService(repo repoiface.AlgorithmRepository, verSvc service.VersionService, storage filestorage.FileStorage) service.AlgorithmService {
	return &algorithmService{repo: repo, verSvc: verSvc, storage: storage}
}

// codePattern 算法唯一标识符只允许字母、数字
var codePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// namePattern 算法名称只允许中文、字母、数字（Go 正则不支持 \u 转义，用字面量区间 一-龥 覆盖 CJK 统一表意文字）
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9一-龥]+$`)

func (s *algorithmService) Register(ctx context.Context, req request.RegisterAlgorithmRequest) (*entity.Algorithm, error) {
	if err := s.validateRegister(req); err != nil {
		return nil, err
	}

	algo := &entity.Algorithm{
		ID:          utils.NewID("algo"),
		Name:        strings.TrimSpace(req.Name),
		Code:        strings.TrimSpace(req.Code),
		Description: strings.TrimSpace(req.Description),
		Category:    strings.TrimSpace(req.Category),
		Author:      strings.TrimSpace(req.Author),
		Language:    enum.Language(strings.TrimSpace(req.Language)),
		Scene:       strings.TrimSpace(req.Scene),
		Stateful:    req.Stateful,
		Port:        req.Port,
		Files:       []entity.AlgorithmFile{},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.Create(algo); err != nil {
		logger.Error("register algorithm failed", "error", err)
		return nil, err
	}

	return algo, nil
}

func (s *algorithmService) Update(id string, req request.UpdateAlgorithmRequest) (*entity.Algorithm, error) {
	algo, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	// 编辑算法仅允许修改：名称、分类、应用场景、更新人、描述
	if req.Name != "" {
		name := strings.TrimSpace(req.Name)
		if !namePattern.MatchString(name) {
			return nil, errors.New("algorithm name must contain only Chinese characters, letters and numbers")
		}
		algo.Name = name
	}
	if req.Category != "" {
		cat := enum.Category(strings.TrimSpace(req.Category))
		if !enum.IsValidCategory(cat) {
			return nil, errors.New("algorithm category must be one of: 通用, 采集, 决策")
		}
		algo.Category = string(cat)
	}
	if req.Scene != "" {
		algo.Scene = strings.TrimSpace(req.Scene)
	}
	if req.UpdateUser != "" {
		algo.UpdateUser = strings.TrimSpace(req.UpdateUser)
	}
	if req.Description != "" {
		algo.Description = strings.TrimSpace(req.Description)
	}
	algo.UpdatedAt = time.Now()

	if err := s.repo.Update(algo); err != nil {
		logger.Error("update algorithm failed", "error", err)
		return nil, err
	}
	return algo, nil
}

func (s *algorithmService) Delete(id string) error {
	// 闸门：仅当最新版本状态为草稿/已归档（或尚无任何版本）时才允许删除，
	// 已发布版本不允许删除（需先归档），避免误删在线算法。
	versions, _ := s.verSvc.List(request.ListVersionRequest{AlgorithmID: id, Limit: 1})
	if len(versions) > 0 {
		// ListByAlgorithm 按 created_at DESC 排序，第一条即最新版本
		if versions[0].Status == enum.VersionStatusReleased {
			return errors.New("算法最新版本为已发布状态，不允许删除；请先归档后再删除")
		}
	}

	// 级联删除该算法下所有版本的文件、变更记录与版本记录
	ctx := context.Background()
	versions, _ = s.verSvc.List(request.ListVersionRequest{AlgorithmID: id, Limit: 10000})
	for _, v := range versions {
		for _, f := range v.Files {
			if err := s.storage.Delete(ctx, f.Path); err != nil {
				logger.Warn("delete version file failed", "key", f.Path, "error", err)
			}
		}
		if err := s.verSvc.Delete(v.ID); err != nil {
			logger.Warn("delete version failed", "id", v.ID, "error", err)
		}
	}
	return s.repo.Delete(id)
}

func (s *algorithmService) GetByID(id string) (*entity.Algorithm, error) {
	return s.repo.GetByID(id)
}

func (s *algorithmService) GetByCode(code string) (*entity.Algorithm, error) {
	return s.repo.GetByCode(code)
}

func (s *algorithmService) List(req request.ListAlgorithmRequest) ([]entity.Algorithm, int) {
	if req.Limit <= 0 {
		req.Limit = 20
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	return s.repo.List(request.AlgorithmFilter{
		Name:   req.Name,
		Code:   req.Code,
		Limit:  req.Limit,
		Offset: req.Offset,
	})
}

func (s *algorithmService) validateRegister(req request.RegisterAlgorithmRequest) error {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return errors.New("algorithm name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New("algorithm name must contain only Chinese characters, letters and numbers")
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		return errors.New("algorithm code is required")
	}
	if !codePattern.MatchString(code) {
		return errors.New("algorithm code must be lowercase and contain only letters, numbers, dashes and underscores (Dapr app-id rule: ^[a-z0-9_-]+$)")
	}

	cat := enum.Category(strings.TrimSpace(req.Category))
	if !enum.IsValidCategory(cat) {
		return errors.New("algorithm category must be one of: 通用, 采集, 决策")
	}

	author := strings.TrimSpace(req.Author)
	if author == "" {
		return errors.New("algorithm author is required")
	}
	if !namePattern.MatchString(author) {
		return errors.New("algorithm author must contain only Chinese characters, letters and numbers")
	}

	lang := enum.Language(strings.TrimSpace(req.Language))
	if !enum.IsValidLanguage(lang) {
		return errors.New("algorithm language must be one of: Java, Python, C++, C#, Go")
	}

	return nil
}
