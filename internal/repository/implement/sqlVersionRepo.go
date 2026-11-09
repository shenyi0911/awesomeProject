package implement

import (
	dbUtils "awesomeProject/internal/repository/utils"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/repository/mapper"
	"awesomeProject/internal/repository/service"
	"awesomeProject/pkg/utils"
)

// sqlVersionRepo 版本仓储的 PostgreSQL 实现
// SQL 全部声明在 mapper/sql/version.sql（-- name: Xxx 标注），此处只负责取语句、传参、扫描。
type sqlVersionRepo struct {
	db *sql.DB
}

// NewSQLVersionRepository 创建版本 SQL 仓储
func NewSQLVersionRepository(db *sql.DB) service.VersionRepository {
	return &sqlVersionRepo{db: db}
}

// scanVersion 从一行扫描出版本实体（不含文件，文件由 loadFiles 单独加载）
func scanVersion(scan func(...any) error) (*entity.AlgorithmVersion, error) {
	v := &entity.AlgorithmVersion{}
	var status, desc, entryPoint, paramSpec, releaseNote sql.NullString
	if err := scan(&v.ID, &v.AlgorithmID, &v.Version, &desc, &status,
		&v.IsActive, &entryPoint, &paramSpec, &releaseNote, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return nil, err
	}
	v.Status = enum.VersionStatus(status.String)
	v.Description = desc.String
	v.EntryPoint = entryPoint.String
	v.ParameterSpec = paramSpec.String
	v.ReleaseNote = releaseNote.String
	return v, nil
}

func (r *sqlVersionRepo) loadFiles(versionID string) []entity.AlgorithmFile {
	rows, err := r.db.Query(mapper.SQL("VersionFileLoadByVersionID"), versionID)
	if err != nil {
		return []entity.AlgorithmFile{}
	}
	defer rows.Close()

	var files []entity.AlgorithmFile
	for rows.Next() {
		var f entity.AlgorithmFile
		var fid, vid, aid, name, path, ftype, ctype sql.NullString
		var size sql.NullInt64
		if err := rows.Scan(&fid, &vid, &aid, &name, &path, &size, &ftype, &ctype); err != nil {
			continue
		}
		f.Name = name.String
		f.Path = path.String
		f.Size = size.Int64
		f.Type = ftype.String
		f.ContentType = ctype.String
		files = append(files, f)
	}
	if files == nil {
		files = []entity.AlgorithmFile{}
	}
	return files
}

// saveFiles 事务内先清空版本文件，再写入当前 Files 数组（幂等，保证表与内存对象一致）。
func (r *sqlVersionRepo) saveFiles(tx *sql.Tx, versionID, algorithmID string, files []entity.AlgorithmFile) error {
	if _, err := tx.Exec(mapper.SQL("VersionFileDeleteByVersionID"), versionID); err != nil {
		return fmt.Errorf("delete old files failed: %w", err)
	}
	for _, f := range files {
		if _, err := tx.Exec(mapper.SQL("VersionFileInsert"),
			utils.NewID("f"), versionID, algorithmID, f.Name, f.Path, f.Size, f.Type, f.ContentType, time.Now(),
		); err != nil {
			return fmt.Errorf("insert file %s failed: %w", f.Name, err)
		}
	}
	return nil
}

func (r *sqlVersionRepo) Create(v *entity.AlgorithmVersion) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(mapper.SQL("VersionCreate"),
		v.ID, v.AlgorithmID, v.Version, v.Description, string(v.Status),
		v.IsActive, v.EntryPoint, v.ParameterSpec, v.ReleaseNote, v.CreatedAt, v.UpdatedAt,
	); err != nil {
		if isUniqueViolation(err) {
			return errors.New("version already exists: " + v.Version)
		}
		return err
	}
	if err := r.saveFiles(tx, v.ID, v.AlgorithmID, v.Files); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sqlVersionRepo) Update(v *entity.AlgorithmVersion) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(mapper.SQL("VersionUpdate"),
		v.ID, v.Description, string(v.Status), v.IsActive, v.EntryPoint, v.ParameterSpec, v.ReleaseNote, time.Now(),
	)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errors.New("version not found: " + v.ID)
	}
	if err := r.saveFiles(tx, v.ID, v.AlgorithmID, v.Files); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sqlVersionRepo) Delete(id string) error {
	res, err := r.db.Exec(mapper.SQL("VersionDelete"), id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errors.New("version not found: " + id)
	}
	return nil
}

func (r *sqlVersionRepo) GetByID(id string) (*entity.AlgorithmVersion, error) {
	row := r.db.QueryRow(mapper.SQL("VersionGetByID"), id)
	v, err := scanVersion(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("version not found: " + id)
		}
		return nil, err
	}
	v.Files = r.loadFiles(v.ID)
	return v, nil
}

// ListByAlgorithm 分页查询某算法的版本列表。
// 动态过滤由 SQL 端「空串即忽略」惯例实现（version/status 空串时条件自动放行）。
func (r *sqlVersionRepo) ListByAlgorithm(algorithmID string, filter request.VersionFilter) ([]entity.AlgorithmVersion, int) {
	var total int
	if err := r.db.QueryRow(mapper.SQL("VersionCountByAlgorithm"),
		algorithmID, filter.Version, string(filter.Status)).Scan(&total); err != nil {
		return []entity.AlgorithmVersion{}, 0
	}

	lim := dbUtils.NormalizeLimit(filter.Limit)
	off := dbUtils.NormalizeOffset(filter.Offset)

	rows, err := r.db.Query(mapper.SQL("VersionListByAlgorithm"),
		algorithmID, filter.Version, string(filter.Status), lim, off)
	if err != nil {
		return []entity.AlgorithmVersion{}, total
	}
	defer rows.Close()

	var result []entity.AlgorithmVersion
	for rows.Next() {
		v, err := scanVersion(rows.Scan)
		if err != nil {
			continue
		}
		v.Files = r.loadFiles(v.ID)
		result = append(result, *v)
	}
	if result == nil {
		result = []entity.AlgorithmVersion{}
	}
	return result, total
}

func (r *sqlVersionRepo) GetActiveByAlgorithm(algorithmID string) (*entity.AlgorithmVersion, error) {
	row := r.db.QueryRow(mapper.SQL("VersionGetActiveByAlgorithm"), algorithmID)
	v, err := scanVersion(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("no active version for algorithm: " + algorithmID)
		}
		return nil, err
	}
	v.Files = r.loadFiles(v.ID)
	return v, nil
}

// SetActive 将目标版本设为 active+released，同算法其他版本 is_active 置 false。事务保证原子性。
func (r *sqlVersionRepo) SetActive(algorithmID, versionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	if _, err := tx.Exec(mapper.SQL("VersionClearActive"), now, algorithmID); err != nil {
		return err
	}
	res, err := tx.Exec(mapper.SQL("VersionSetActive"),
		string(enum.VersionStatusReleased), now, versionID, algorithmID)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return errors.New("version not belong to algorithm: " + versionID)
	}
	return tx.Commit()
}

// DeleteChangeLogs 删除某版本的全部变更记录（级联删除版本时调用）
func (r *sqlVersionRepo) DeleteChangeLogs(versionID string) error {
	_, err := r.db.Exec(mapper.SQL("ChangeLogDeleteByVersionID"), versionID)
	return err
}

func (r *sqlVersionRepo) CreateChangeLog(log *entity.VersionChangeLog) error {
	_, err := r.db.Exec(mapper.SQL("ChangeLogCreate"),
		log.ID, log.AlgorithmID, log.VersionID, log.Version, log.Operator, log.Action, log.ChangeTime, log.ChangeDesc, log.FileMD5,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil // 变更记录幂等写入，重复忽略
		}
		return err
	}
	return nil
}

func (r *sqlVersionRepo) ListChangeLog(versionID string, limit, offset int) ([]entity.VersionChangeLog, int, error) {
	var total int
	if err := r.db.QueryRow(mapper.SQL("ChangeLogCountByVersionID"), versionID).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.Query(mapper.SQL("ChangeLogListByVersionID"), versionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var logs []entity.VersionChangeLog
	for rows.Next() {
		var l entity.VersionChangeLog
		var version, operator, action, desc, md5 sql.NullString
		if err := rows.Scan(&l.ID, &l.AlgorithmID, &l.VersionID, &version, &operator, &action, &l.ChangeTime, &desc, &md5); err != nil {
			continue
		}
		l.Version = version.String
		l.Operator = operator.String
		l.Action = action.String
		l.ChangeDesc = desc.String
		l.FileMD5 = md5.String
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []entity.VersionChangeLog{}
	}
	return logs, total, nil
}
