package mapper

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

// sqlMapperFS 内嵌全部 SQL mapper 文件（MyBatis 风格：SQL 与 Go 代码分离）。
// DML 在 sql/algorithm.sql、sql/version.sql、sql/session.sql 中以 `-- name: Xxx` 标注；
// 建表 DDL 独立放在 sql/schema.sql（不走 name 注册，由 migrateSchema 按分号切分执行）。
//
//go:embed sql/*.sql
var sqlMapperFS embed.FS

var (
	sqlLoadOnce sync.Once
	sqlRegistry map[string]string
)

// SQL 按名称获取 mapper 中声明的 SQL 语句（对应 MyBatis 的 sqlSession.getStatement）。
// 未注册的名称直接 panic——拼写错误在首次触达即暴露，而非把空 SQL 静默发给数据库。
func SQL(name string) string {
	sqlLoadOnce.Do(loadSQLMappers)
	q, ok := sqlRegistry[name]
	if !ok {
		panic(fmt.Sprintf("sql query not registered in sql/*.sql: %s", name))
	}
	return q
}

// SchemaDDL 返回建表 DDL 全文（sql/schema.sql），供仓储层 migrateSchema 执行。
func SchemaDDL() string {
	data, err := sqlMapperFS.ReadFile("sql/schema.sql")
	if err != nil {
		panic(fmt.Sprintf("read sql/schema.sql failed: %v", err))
	}
	return string(data)
}

// loadSQLMappers 解析 sql/*.sql（除 schema.sql），按 `-- name: Xxx` 标注构建 名称→SQL 映射。
func loadSQLMappers() {
	entries, err := sqlMapperFS.ReadDir("sql")
	if err != nil {
		panic(fmt.Sprintf("read embedded sql dir failed: %v", err))
	}
	sqlRegistry = make(map[string]string, 32)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() == "schema.sql" {
			continue
		}
		data, err := sqlMapperFS.ReadFile("sql/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("read sql/%s failed: %v", e.Name(), err))
		}
		parseSQLFile(e.Name(), string(data))
	}
}

// parseSQLFile 单文件解析：`-- name: Xxx` 开启新语句，其后各行归入该语句，直至下一个标注。
func parseSQLFile(file, content string) {
	var (
		name string
		buf  strings.Builder
	)
	flush := func() {
		if name == "" {
			return
		}
		q := strings.TrimSpace(buf.String())
		n := name
		name = ""
		buf.Reset()
		if q == "" {
			return
		}
		if _, dup := sqlRegistry[n]; dup {
			panic(fmt.Sprintf("duplicate sql name %q in sql/%s", n, file))
		}
		sqlRegistry[n] = q
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "-- name:") {
			flush()
			name = strings.TrimSpace(strings.TrimPrefix(trimmed, "-- name:"))
			continue
		}
		if name != "" {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	flush()
}
