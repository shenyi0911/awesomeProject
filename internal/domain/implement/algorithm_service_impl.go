package implement

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/domain/errs"
	"awesomeProject/internal/domain/manifest"
	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/domain/service"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

type algorithmService struct {
	repo        port.AlgorithmRepository
	verSvc      service.VersionService
	sessSvc     service.SessionService
	storage     port.FileStorage
	rt          port.Runtime
	metaInvalid func(code string)
}

// NewAlgorithmService creates the algorithm service.
func NewAlgorithmService(repo port.AlgorithmRepository, verSvc service.VersionService, sessSvc service.SessionService, storage port.FileStorage, rt port.Runtime) *algorithmService {
	return &algorithmService{repo: repo, verSvc: verSvc, sessSvc: sessSvc, storage: storage, rt: rt}
}

// SetMetaInvalidator registers a callback that drops cached algorithm metadata after updates or deletion.
func (s *algorithmService) SetMetaInvalidator(fn func(code string)) {
	s.metaInvalid = fn
}

// codePattern allows only lowercase letters, digits, underscores and dashes (Dapr app-id rule).
var codePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// namePattern allows Chinese characters, letters, digits, spaces and common punctuation.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9一-龥 _.()（）&'-]+$`)

// Column length limits mirrored from schema.sql so oversized input returns 400 instead of a DB error.
const (
	maxAlgorithmCodeLen = 64
	maxAlgorithmNameLen = 255
	maxAuthorLen        = 128
)

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
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.Create(ctx, algo); err != nil {
		logger.Error("register algorithm failed", "error", err)
		return nil, err
	}
	return algo, nil
}

func (s *algorithmService) Update(ctx context.Context, id string, req request.UpdateAlgorithmRequest) (*entity.Algorithm, error) {
	algo, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Only name, category, scene, update user and description can be modified.
	if req.Name != "" {
		name := strings.TrimSpace(req.Name)
		if err := validateAlgorithmName(name); err != nil {
			return nil, err
		}
		algo.Name = name
	}
	if req.Category != "" {
		cat := enum.Category(strings.TrimSpace(req.Category))
		if !enum.IsValidCategory(cat) {
			return nil, errs.New(errs.KindBadRequest, "algorithm category must be one of: 通用, 采集, 决策")
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

	if err := s.repo.Update(ctx, algo); err != nil {
		logger.Error("update algorithm failed", "error", err)
		return nil, err
	}
	if s.metaInvalid != nil {
		s.metaInvalid(algo.Code)
	}
	return algo, nil
}

func (s *algorithmService) Delete(ctx context.Context, id string) error {
	algo, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	versions, err := s.verSvc.ListAll(ctx, id)
	if err != nil {
		return err
	}
	// Guard: an active released version must be archived first, preventing deletion of a live algorithm.
	for _, v := range versions {
		if v.IsActive && v.Status == enum.VersionStatusReleased {
			return errs.New(errs.KindConflict, "algorithm has a released version and cannot be deleted; archive it first")
		}
	}

	// Delete database rows first (the schema cascades versions, files and change logs), then remove
	// objects best-effort. A storage failure leaves an orphan object instead of dangling references.
	var objectKeys []string
	for _, v := range versions {
		for _, f := range v.Files {
			objectKeys = append(objectKeys, f.Path)
		}
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	for _, key := range objectKeys {
		if err := s.storage.Delete(ctx, key); err != nil {
			logger.Warn("delete version file failed", "key", key, "error", err)
		}
	}
	if s.sessSvc != nil {
		if err := s.sessSvc.DeleteByCode(ctx, algo.Code); err != nil {
			logger.Warn("delete algorithm sessions failed", "code", algo.Code, "error", err)
		}
	}
	if s.metaInvalid != nil {
		s.metaInvalid(algo.Code)
	}
	if s.rt != nil {
		s.rt.InvalidateRelease(algo.Code)
		s.rt.Kill(algo.Code)
	}
	return nil
}

func (s *algorithmService) GetByID(ctx context.Context, id string) (*entity.Algorithm, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *algorithmService) GetByCode(ctx context.Context, code string) (*entity.Algorithm, error) {
	return s.repo.GetByCode(ctx, code)
}

// GetContract returns the structured method contract of the algorithm's currently active released version.
func (s *algorithmService) GetContract(ctx context.Context, code string) ([]manifest.MethodSpec, error) {
	algo, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	ver, err := s.verSvc.GetActive(ctx, algo.ID)
	if err != nil {
		// No active version means no contract (not published); real failures must surface.
		if errs.KindOf(err) == errs.KindNotFound {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(ver.ParameterSpec) == "" {
		return nil, nil
	}
	var methods []manifest.MethodSpec
	if err := json.Unmarshal([]byte(ver.ParameterSpec), &methods); err != nil {
		logger.Warn("GetContract: parse parameter_spec failed", "code", code, "error", err)
		return nil, nil
	}
	return methods, nil
}

func (s *algorithmService) List(ctx context.Context, req request.ListAlgorithmRequest) ([]entity.Algorithm, int, error) {
	limit, offset := utils.NormalizePaging(req.Limit, req.Offset)
	return s.repo.List(ctx, request.AlgorithmFilter{
		Name:   req.Name,
		Code:   req.Code,
		Limit:  limit,
		Offset: offset,
	})
}

// validateAlgorithmName validates the display name and its column length.
func validateAlgorithmName(name string) error {
	if name == "" {
		return errs.New(errs.KindBadRequest, "algorithm name is required")
	}
	if len(name) > maxAlgorithmNameLen {
		return errs.New(errs.KindBadRequest, "algorithm name is too long")
	}
	if !namePattern.MatchString(name) {
		return errs.New(errs.KindBadRequest, "algorithm name may contain only Chinese characters, letters, numbers, spaces and _ . ( ) & ' -")
	}
	return nil
}

func (s *algorithmService) validateRegister(req request.RegisterAlgorithmRequest) error {
	name := strings.TrimSpace(req.Name)
	if err := validateAlgorithmName(name); err != nil {
		return err
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		return errs.New(errs.KindBadRequest, "algorithm code is required")
	}
	if len(code) > maxAlgorithmCodeLen {
		return errs.New(errs.KindBadRequest, "algorithm code is too long")
	}
	if !codePattern.MatchString(code) {
		return errs.New(errs.KindBadRequest, "algorithm code must be lowercase and contain only letters, numbers, dashes and underscores (Dapr app-id rule: ^[a-z0-9_-]+$)")
	}

	cat := enum.Category(strings.TrimSpace(req.Category))
	if !enum.IsValidCategory(cat) {
		return errs.New(errs.KindBadRequest, "algorithm category must be one of: 通用, 采集, 决策")
	}

	author := strings.TrimSpace(req.Author)
	if author == "" {
		return errs.New(errs.KindBadRequest, "algorithm author is required")
	}
	if len(author) > maxAuthorLen {
		return errs.New(errs.KindBadRequest, "algorithm author is too long")
	}
	if !namePattern.MatchString(author) {
		return errs.New(errs.KindBadRequest, "algorithm author may contain only Chinese characters, letters, numbers, spaces and _ . ( ) & ' -")
	}

	lang := enum.Language(strings.TrimSpace(req.Language))
	if !enum.IsValidLanguage(lang) {
		return errs.New(errs.KindBadRequest, "algorithm language must be one of: Java, Python, C++, C#, Go")
	}
	return nil
}
