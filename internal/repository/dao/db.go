package dao

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "github.com/lib/pq"

	"awesomeProject/internal/repository/mapper"
)

// OpenSQLDB 打开数据库连接，校验连通性，并自动建表（幂等）。
// driver 仅支持 "postgres"；dsn 为 PostgreSQL 连接串。
// 返回的 *sql.DB 由调用方长期持有、程序退出前 Close。
func OpenSQLDB(driver, dsn string) (*sql.DB, error) {
	if driver == "" {
		driver = "postgres"
	}
	if driver != "postgres" {
		return nil, fmt.Errorf("unsupported sql driver: %s", driver)
	}
	if dsn == "" {
		return nil, errors.New("sql dsn is required when repository.type=sql")
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sql db failed: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sql db failed: %w", err)
	}
	if err := migrateSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema failed: %w", err)
	}
	return db, nil
}

// migrateSchema 执行建表 DDL（幂等）。DDL 全文在 mapper/sql/schema.sql，
// 此处按分号切分逐条执行（DDL 无存储过程/字符串分号，切分安全）。
func migrateSchema(db *sql.DB) error {
	for i, stmt := range strings.Split(mapper.SchemaDDL(), ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("exec schema stmt #%d failed: %w", i+1, err)
		}
	}
	return nil
}
