package implement

import (
	"awesomeProject/internal/repository/utils"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"

	"awesomeProject/internal/domain/do/entity"
	"awesomeProject/internal/domain/do/request"
	"awesomeProject/internal/domain/enum"
	"awesomeProject/internal/repository/mapper"
	"awesomeProject/internal/repository/service"
)

// sqlAlgorithmRepo 算法仓储的 PostgreSQL 实现
// SQL 全部声明在 mapper/sql/algorithm.sql（-- name: Xxx 标注），此处只负责取语句、传参、扫描。
type sqlAlgorithmRepo struct {
	db *sql.DB
}

// NewSQLAlgorithmRepository 创建算法 SQL 仓储
func NewSQLAlgorithmRepository(db *sql.DB) service.AlgorithmRepository {
	return &sqlAlgorithmRepo{db: db}
}

// isUniqueViolation 判断是否为唯一约束冲突（PostgreSQL 错误码 23505）
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return false
}

// ErrAlgorithmNotFound 算法不存在
type ErrAlgorithmNotFound struct{ ID string }

func (e ErrAlgorithmNotFound) Error() string {
	if e.ID == "" {
		return "algorithm not found"
	}
	return "algorithm not found: " + e.ID
}

func (r *sqlAlgorithmRepo) Create(algo *entity.Algorithm) error {
	_, err := r.db.Exec(mapper.SQL("AlgorithmCreate"),
		algo.ID, algo.Name, algo.Code, algo.Description, algo.Category,
		algo.Author, string(algo.Language), algo.Scene, algo.Stateful,
		algo.UpdateUser, algo.Port, algo.CreatedAt, algo.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return errors.New("algorithm code already exists: " + algo.Code)
		}
		return err
	}
	return nil
}

func (r *sqlAlgorithmRepo) Update(algo *entity.Algorithm) error {
	res, err := r.db.Exec(mapper.SQL("AlgorithmUpdate"),
		algo.ID, algo.Name, algo.Code, algo.Description, algo.Category,
		algo.Author, string(algo.Language), algo.Scene, algo.Stateful,
		algo.Port, algo.UpdateUser, time.Now(),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return errors.New("algorithm code already exists: " + algo.Code)
		}
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrAlgorithmNotFound{ID: algo.ID}
	}
	return nil
}

func (r *sqlAlgorithmRepo) Delete(id string) error {
	res, err := r.db.Exec(mapper.SQL("AlgorithmDelete"), id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrAlgorithmNotFound{ID: id}
	}
	return nil
}

func (r *sqlAlgorithmRepo) GetByID(id string) (*entity.Algorithm, error) {
	a := &entity.Algorithm{}
	var name, code, desc, category, author, lang, scene, updateUser sql.NullString
	if err := r.db.QueryRow(mapper.SQL("AlgorithmGetByID"), id).
		Scan(&a.ID, &name, &code, &desc, &category, &author, &lang, &scene,
			&a.Stateful, &updateUser, &a.Port, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAlgorithmNotFound{ID: id}
		}
		return nil, err
	}
	a.Name = name.String
	a.Code = code.String
	a.Description = desc.String
	a.Category = category.String
	a.Author = author.String
	a.Language = enum.Language(lang.String)
	a.Scene = scene.String
	a.UpdateUser = updateUser.String
	// Files 已下沉到版本维度（version_files 表），算法表不再存储文件列表。
	a.Files = []entity.AlgorithmFile{}
	return a, nil
}

func (r *sqlAlgorithmRepo) GetByCode(code string) (*entity.Algorithm, error) {
	a := &entity.Algorithm{}
	var name, c, desc, category, author, lang, scene, updateUser sql.NullString
	if err := r.db.QueryRow(mapper.SQL("AlgorithmGetByCode"), code).
		Scan(&a.ID, &name, &c, &desc, &category, &author, &lang, &scene,
			&a.Stateful, &updateUser, &a.Port, &a.CreatedAt, &a.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrAlgorithmNotFound{}
		}
		return nil, err
	}
	a.Name = name.String
	a.Code = c.String
	a.Description = desc.String
	a.Category = category.String
	a.Author = author.String
	a.Language = enum.Language(lang.String)
	a.Scene = scene.String
	a.UpdateUser = updateUser.String
	a.Files = []entity.AlgorithmFile{}
	return a, nil
}

// List 分页查询算法列表（附最新版本号与状态）。
// 动态过滤由 SQL 端「空串即忽略」惯例实现：name/code 空串时条件自动放行，参数恒传、索引固定。
func (r *sqlAlgorithmRepo) List(filter request.AlgorithmFilter) ([]entity.Algorithm, int) {
	var total int
	if err := r.db.QueryRow(mapper.SQL("AlgorithmCountByFilter"), filter.Name, filter.Code).Scan(&total); err != nil {
		return []entity.Algorithm{}, 0
	}

	lim := utils.NormalizeLimit(filter.Limit)
	off := utils.NormalizeOffset(filter.Offset)

	rows, err := r.db.Query(mapper.SQL("AlgorithmListByFilter"), filter.Name, filter.Code, lim, off)
	if err != nil {
		return []entity.Algorithm{}, total
	}
	defer rows.Close()

	var result []entity.Algorithm
	for rows.Next() {
		var a entity.Algorithm
		var name, code, desc, category, author, lang, scene, updateUser sql.NullString
		var latestVersion, latestVersionStatus string
		if err := rows.Scan(&a.ID, &name, &code, &desc, &category, &author,
			&lang, &scene, &a.Stateful, &updateUser, &a.Port, &a.CreatedAt, &a.UpdatedAt,
			&latestVersion, &latestVersionStatus); err != nil {
			continue
		}
		a.Name = name.String
		a.Code = code.String
		a.Description = desc.String
		a.Category = category.String
		a.Author = author.String
		a.Language = enum.Language(lang.String)
		a.Scene = scene.String
		a.UpdateUser = updateUser.String
		a.LatestVersion = latestVersion
		a.LatestVersionStatus = latestVersionStatus
		a.Files = []entity.AlgorithmFile{}
		result = append(result, a)
	}
	if result == nil {
		result = []entity.Algorithm{}
	}
	return result, total
}

// ExistsByPort 判断是否存在「监听端口=port」的算法（排除 excludeID 自身），用于端口唯一性校验。
// excludeID 为空串时按全表校验（SQL 端 ($2=” OR id<>$2) 自动放行）。
func (r *sqlAlgorithmRepo) ExistsByPort(port int, excludeID string) (bool, error) {
	var cnt int
	if err := r.db.QueryRow(mapper.SQL("AlgorithmExistsByPort"), port, excludeID).Scan(&cnt); err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// PortForCode 按 code 返回平台指定的算法监听端口（运行时 PortResolver 注入用）。
func (r *sqlAlgorithmRepo) PortForCode(ctx context.Context, code string) (int, error) {
	var port int
	if err := r.db.QueryRow(mapper.SQL("AlgorithmPortForCode"), code).Scan(&port); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrAlgorithmNotFound{}
		}
		return 0, err
	}
	return port, nil
}

// SetPort 写回运行时实际分配的监听端口（按主机动态分配）。
func (r *sqlAlgorithmRepo) SetPort(ctx context.Context, code string, port int) error {
	res, err := r.db.ExecContext(ctx, mapper.SQL("AlgorithmSetPort"), port, code)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrAlgorithmNotFound{ID: code}
	}
	return nil
}
