package mapper

import (
	"embed"
	"fmt"
	"strings"
	"sync"
)

// sqlMapperFS embeds all SQL mapper files (MyBatis style: SQL separated from Go code).
// DML in sql/algorithm.sql, sql/version.sql and sql/session.sql is annotated with `-- name: Xxx`;
// the schema DDL lives in sql/schema.sql (not registered by name; migrateSchema splits and executes it).
//
//go:embed sql/*.sql
var sqlMapperFS embed.FS

var (
	sqlLoadOnce sync.Once
	sqlRegistry map[string]string
)

// SQL returns the statement registered under name (the MyBatis sqlSession.getStatement equivalent).
// An unknown name panics immediately, so typos surface on first use instead of sending empty SQL to the database.
func SQL(name string) string {
	sqlLoadOnce.Do(loadSQLMappers)
	q, ok := sqlRegistry[name]
	if !ok {
		panic(fmt.Sprintf("sql query not registered in sql/*.sql: %s", name))
	}
	return q
}

// SchemaDDL returns the full schema DDL (sql/schema.sql) for migrateSchema in the repository layer.
func SchemaDDL() string {
	data, err := sqlMapperFS.ReadFile("sql/schema.sql")
	if err != nil {
		panic(fmt.Sprintf("read sql/schema.sql failed: %v", err))
	}
	return string(data)
}

// loadSQLMappers parses sql/*.sql (except schema.sql) and builds a name -> SQL map from `-- name: Xxx` annotations.
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

// parseSQLFile parses one file: `-- name: Xxx` starts a new statement, and subsequent lines belong
// to it until the next annotation.
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
