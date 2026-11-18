package implement

import (
	"context"
	"database/sql"
	"errors"

	"awesomeProject/internal/domain/port"
	"awesomeProject/internal/repository/mapper"
)

// LatestReleasedVersion returns (versionID, version, status) of the latest version for code.
// When no version exists it returns empty strings without an error; callers apply fail-closed handling.
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

// VersionFiles returns the object storage file list of a version (Key is the object key, RelPath the relative path).
func (r *sqlVersionRepo) VersionFiles(ctx context.Context, versionID string) ([]port.FileRef, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("VersionFileListRefs"), versionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []port.FileRef{}
	for rows.Next() {
		var name, path sql.NullString
		if err := rows.Scan(&name, &path); err != nil {
			return nil, err
		}
		out = append(out, port.FileRef{Key: path.String, RelPath: name.String})
	}
	return out, rows.Err()
}

// ListReleasedCodes returns all algorithm codes whose latest version is released (used to restore
// running instances at startup).
func (r *sqlVersionRepo) ListReleasedCodes(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, mapper.SQL("RuntimeListReleasedCodes"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	codes := []string{}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}
