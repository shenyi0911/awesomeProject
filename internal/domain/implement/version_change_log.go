package implement

import (
	"context"
	"strings"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/pkg/logger"
	"awesomeProject/pkg/utils"
)

func (s *versionService) ListChangeLog(ctx context.Context, versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error) {
	limit, offset = utils.NormalizePaging(limit, offset)
	return s.verRepo.ListChangeLog(ctx, versionID, limit, offset)
}

// logChange writes a version change log entry.
func (s *versionService) logChange(ctx context.Context, v *entity.AlgorithmVersion, operator, action, desc, fileMD5 string) {
	entry := &entity.VersionChangeLog{
		ID:          utils.NewID("log"),
		AlgorithmID: v.AlgorithmID,
		VersionID:   v.ID,
		Version:     v.Version,
		Operator:    operator,
		Action:      action,
		ChangeTime:  time.Now(),
		ChangeDesc:  desc,
		FileMD5:     fileMD5,
	}
	if err := s.verRepo.CreateChangeLog(ctx, entry); err != nil {
		logger.Warn("write version change log failed", "error", err)
	}
}

func normalizeOperator(op string) string {
	op = strings.TrimSpace(op)
	if op == "" {
		return "system"
	}
	return op
}
