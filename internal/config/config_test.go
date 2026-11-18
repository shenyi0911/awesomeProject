package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaultsAndEnvExpansion(t *testing.T) {
	t.Setenv("AWESOME_TEST_PORT", "9091")
	t.Setenv("AWESOME_TEST_DSN", "postgres://localhost/test")
	t.Setenv("AWESOME_TEST_ACCESS_KEY", "ak")
	t.Setenv("AWESOME_TEST_SECRET_KEY", "sk")

	path := writeConfig(t, `
server:
  port: ${AWESOME_TEST_PORT}
  readTimeout: 2m
  corsAllowOrigins:
    - http://localhost:5173
repository:
  sql:
    dsn: "${AWESOME_TEST_DSN}"
fileStorage:
  s3:
    accessKey: "${AWESOME_TEST_ACCESS_KEY}"
    secretKey: "${AWESOME_TEST_SECRET_KEY}"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 9091 {
		t.Fatalf("Port = %d, want 9091", cfg.Server.Port)
	}
	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("Host = %q, want default 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.ReadTimeout.D() != 2*time.Minute {
		t.Fatalf("ReadTimeout = %v, want 2m", cfg.Server.ReadTimeout.D())
	}
	if cfg.Server.MaxJSONBody != 8<<20 {
		t.Fatalf("MaxJSONBody = %d, want %d", cfg.Server.MaxJSONBody, 8<<20)
	}
	if cfg.Server.MaxUploadBody != 1<<30 {
		t.Fatalf("MaxUploadBody = %d, want %d", cfg.Server.MaxUploadBody, 1<<30)
	}
	if cfg.Server.MaxFileBytes != 256<<20 {
		t.Fatalf("MaxFileBytes = %d, want %d", cfg.Server.MaxFileBytes, 256<<20)
	}
	if cfg.Server.MaxMultipartMemory != 8<<20 {
		t.Fatalf("MaxMultipartMemory = %d, want %d", cfg.Server.MaxMultipartMemory, 8<<20)
	}
	if len(cfg.Server.CORSAllowOrigins) != 1 || cfg.Server.CORSAllowOrigins[0] != "http://localhost:5173" {
		t.Fatalf("CORSAllowOrigins = %v", cfg.Server.CORSAllowOrigins)
	}
	if cfg.Repository.Type != "sql" || cfg.Repository.SQL.Driver != "postgres" {
		t.Fatalf("repository defaults = %+v", cfg.Repository)
	}
	if cfg.Repository.SQL.MaxOpenConns != 20 || cfg.Repository.SQL.MaxIdleConns != 5 {
		t.Fatalf("pool defaults = %+v", cfg.Repository.SQL)
	}
	if cfg.Repository.SQL.ConnMaxLifetime.D() != 30*time.Minute ||
		cfg.Repository.SQL.ConnMaxIdleTime.D() != 5*time.Minute {
		t.Fatalf("pool durations = %+v", cfg.Repository.SQL)
	}
	if cfg.FileStorage.Type != "s3" || cfg.FileStorage.S3.Endpoint != "127.0.0.1:9000" {
		t.Fatalf("file storage defaults = %+v", cfg.FileStorage)
	}
	if cfg.Runtime.ReleaseCacheTTL.D() != 30*time.Second || cfg.Runtime.InvokeTimeout.D() != 60*time.Second {
		t.Fatalf("runtime defaults = %+v", cfg.Runtime)
	}
	if cfg.Session.TTL.D() != 30*time.Minute || cfg.Session.GCInterval.D() != 5*time.Minute {
		t.Fatalf("session defaults = %+v", cfg.Session)
	}
}

func TestLoadEnvPlaceholderDefault(t *testing.T) {
	path := writeConfig(t, `
server:
  port: ${AWESOME_UNSET_PORT_12345:-8092}
repository:
  sql:
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8092 {
		t.Fatalf("Port = %d, want placeholder default 8092", cfg.Server.Port)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{
			name: "missing dsn",
			content: `
repository:
  sql: {}
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`,
		},
		{
			name: "unsupported driver",
			content: `
repository:
  sql:
    driver: mysql
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`,
		},
		{
			name: "missing s3 credentials",
			content: `
repository:
  sql:
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: ""
    secretKey: ""
`,
		},
		{
			name: "invalid server mode",
			content: `
server:
  mode: prod
repository:
  sql:
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`,
		},
		{
			name: "invalid log level",
			content: `
log:
  level: verbose
repository:
  sql:
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tc.content)); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("expected an error for a missing config file")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	content := `
server:
  unknownSetting: true
repository:
  sql:
    dsn: "postgres://localhost/test"
fileStorage:
  s3:
    accessKey: "ak"
    secretKey: "sk"
`
	if _, err := Load(writeConfig(t, content)); err == nil {
		t.Fatal("expected an error for an unknown configuration field")
	}
}
