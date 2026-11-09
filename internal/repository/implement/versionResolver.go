package implement

import (
	"context"
	"database/sql"
	"errors"

	"awesomeProject/internal/repository/mapper"
	"awesomeProject/internal/runtime"
)

// Compile-time 断言：*sqlVersionRepo 满足 runtime.VersionResolver（运行时从 MinIO 拉起算法所需）。
var _ runtime.VersionResolver = (*sqlVersionRepo)(nil)

// LatestReleasedVersion 返回该 code 最新版本的 (versionID, version, status)。
// 是否 really released 由调用方判断；本方法只如实返回最新版本的真实状态。
func (r *sqlVersionRepo) LatestReleasedVersion(ctx context.Context, code string) (versionID, ver, status string, err error) {
	row := r.db.QueryRowContext(ctx, mapper.SQL("RuntimeLatestReleasedVersion"), code)
	var vid, v, st sql.NullString
	if e := row.Scan(&vid, &v, &st); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", "", "", nil
		}
		return "", "", "", e
	}
	return vid.String, v.String, st.String, nil
}

// VersionFiles 返回某版本在对象存储中的文件清单（Key=MinIO path，RelPath=相对路径 name）。
func (r *sqlVersionRepo) VersionFiles(ctx context.Context, versionID string) ([]runtime.FileRef, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionFileListRefs"), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []runtime.FileRef
	for rows.Next() {
		var name, path sql.NullString
		if err := rows.Scan(&name, &path); err != nil {
			continue
		}
		out = append(out, runtime.FileRef{Key: path.String, RelPath: name.String})
	}
	if out == nil {
		out = []runtime.FileRef{}
	}
	return out, nil
}

// ListReleasedCodes 返回「最新版本状态=released」的所有算法 code（启动时恢复拉起用）。
// 用 PostgreSQL 的 DISTINCT ON 取每个算法最新版本，再过滤出 released。
func (r *sqlVersionRepo) ListReleasedCodes(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("RuntimeListReleasedCodes"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			continue
		}
		codes = append(codes, code)
	}
	if codes == nil {
		codes = []string{}
	}
	return codes, nil
}
