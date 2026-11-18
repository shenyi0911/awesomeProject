package dao

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"awesomeProject/internal/repository/mapper"
)

// migrationLockKey is a fixed advisory-lock key serializing schema migration across replicas.
const migrationLockKey int64 = 728193461

// OpenSQLDB opens the database, verifies connectivity, and applies the idempotent schema migration.
// ctx bounds connection and migration time. Only "postgres" is supported; dsn is the PostgreSQL connection string.
// The returned *sql.DB is owned by the caller and must be closed before exit.
func OpenSQLDB(ctx context.Context, driver, dsn string) (*sql.DB, error) {
	if driver == "" {
		driver = "postgres"
	}
	if driver != "postgres" {
		return nil, fmt.Errorf("unsupported sql driver: %s", driver)
	}
	if dsn == "" {
		return nil, errors.New("sql dsn is required")
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sql db failed: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sql db failed: %w", err)
	}
	if err := migrateSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema failed: %w", err)
	}
	return db, nil
}

// migrateSchema executes the idempotent DDL from mapper/sql/schema.sql. A session-scoped advisory
// lock serializes migration across replicas, and line comments are stripped before splitting by
// semicolons so comments can never be executed as statements.
func migrateSchema(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockKey); err != nil {
		return fmt.Errorf("acquire migration lock failed: %w", err)
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", migrationLockKey)
	}()

	for i, stmt := range splitSQLStatements(mapper.SchemaDDL()) {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("exec schema stmt #%d failed: %w", i+1, err)
		}
	}
	return nil
}

// splitSQLStatements removes comment-only lines and splits the DDL by semicolons.
func splitSQLStatements(ddl string) []string {
	var b strings.Builder
	for _, line := range strings.Split(ddl, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.Split(b.String(), ";")
}
