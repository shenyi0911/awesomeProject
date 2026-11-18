package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that accepts YAML strings such as "30m" and "1h".
type Duration time.Duration

// D returns the value as a time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// UnmarshalYAML parses a duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*d = 0
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Log         LogConfig         `yaml:"log"`
	Repository  RepositoryConfig  `yaml:"repository"`
	FileStorage FileStorageConfig `yaml:"fileStorage"`
	Runtime     RuntimeConfig     `yaml:"runtime"`
	Session     SessionConfig     `yaml:"session"`
	Auth        AuthConfig        `yaml:"auth"`
}

type ServerConfig struct {
	Host            string   `yaml:"host"`
	Port            int      `yaml:"port"`
	Mode            string   `yaml:"mode"`
	ReadTimeout     Duration `yaml:"readTimeout"`
	WriteTimeout    Duration `yaml:"writeTimeout"`
	IdleTimeout     Duration `yaml:"idleTimeout"`
	ShutdownTimeout Duration `yaml:"shutdownTimeout"`
	MaxJSONBody     int64    `yaml:"maxJsonBodyBytes"`
	MaxUploadBody   int64    `yaml:"maxUploadBodyBytes"`

	// CORSAllowOrigins is the CORS allowlist; empty or "*" allows every origin.
	CORSAllowOrigins []string `yaml:"corsAllowOrigins"`
	// MaxFileBytes caps a single uploaded file; <=0 falls back to the default.
	MaxFileBytes int64 `yaml:"maxFileBytes"`
	// MaxMultipartMemory is the in-memory buffer threshold for multipart parsing.
	MaxMultipartMemory int64 `yaml:"maxMultipartMemoryBytes"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// RepositoryConfig configures the persistence layer, currently a direct SQL connection.
type RepositoryConfig struct {
	Type string    `yaml:"type"`
	SQL  SQLConfig `yaml:"sql"`
}

// SQLConfig configures the SQL database connection and pool.
type SQLConfig struct {
	Driver          string   `yaml:"driver"`
	DSN             string   `yaml:"dsn"`
	MaxOpenConns    int      `yaml:"maxOpenConns"`
	MaxIdleConns    int      `yaml:"maxIdleConns"`
	ConnMaxLifetime Duration `yaml:"connMaxLifetime"`
	ConnMaxIdleTime Duration `yaml:"connMaxIdleTime"`
}

// FileStorageConfig configures file storage.
type FileStorageConfig struct {
	Type string   `yaml:"type"`
	S3   S3Config `yaml:"s3"`
}

// S3Config configures an S3-compatible object store (RustFS, MinIO, SeaweedFS, etc.).
type S3Config struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"accessKey"`
	SecretKey string `yaml:"secretKey"`
	UseSSL    bool   `yaml:"useSSL"`
	Bucket    string `yaml:"bucket"`
}

// RuntimeConfig configures algorithm runtime orchestration.
type RuntimeConfig struct {
	Workspace       string   `yaml:"workspace"`
	DaprBin         string   `yaml:"daprBin"`
	ReleaseCheck    bool     `yaml:"releaseCheck"`
	ReleaseCacheTTL Duration `yaml:"releaseCacheTTL"`
	InvokeTimeout   Duration `yaml:"invokeTimeout"`
	MaxRestarts     int      `yaml:"maxRestarts"`
}

// SessionConfig configures session management.
type SessionConfig struct {
	TTL              Duration `yaml:"ttl"`
	GCInterval       Duration `yaml:"gcInterval"`
	MaxPerAlgorithm  int      `yaml:"maxPerAlgorithm"`
	StatefulCacheTTL Duration `yaml:"statefulCacheTTL"`
}

// AuthConfig configures authentication; an empty token disables it (internal/dev environments only).
// Operator, when set, is recorded as the audit actor for authenticated requests.
type AuthConfig struct {
	Token    string `yaml:"token"`
	Operator string `yaml:"operator"`
}

// Load reads the configuration: it loads .env (if present), expands ${VAR} and ${VAR:-default}
// placeholders in the YAML, then applies defaults and runs validation.
func Load(path string) (*Config, error) {
	loadDotEnv()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s failed: %w", path, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader([]byte(expandEnv(string(data)))))
	// Reject unknown keys so configuration typos fail fast instead of being silently ignored.
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}

	if err := cfg.applyDefaults().validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Addr returns the listen address.
func (c *Config) Addr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) applyDefaults() *Config {
	if c.Server.Host == "" {
		c.Server.Host = "0.0.0.0"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.Mode == "" {
		c.Server.Mode = "debug"
	}
	if c.Server.ReadTimeout <= 0 {
		c.Server.ReadTimeout = Duration(5 * time.Minute)
	}
	if c.Server.WriteTimeout <= 0 {
		c.Server.WriteTimeout = Duration(10 * time.Minute)
	}
	if c.Server.IdleTimeout <= 0 {
		c.Server.IdleTimeout = Duration(60 * time.Second)
	}
	if c.Server.ShutdownTimeout <= 0 {
		c.Server.ShutdownTimeout = Duration(15 * time.Second)
	}
	if c.Server.MaxJSONBody <= 0 {
		c.Server.MaxJSONBody = 8 << 20 // 8 MiB
	}
	if c.Server.MaxUploadBody <= 0 {
		c.Server.MaxUploadBody = 1 << 30 // 1 GiB
	}
	if c.Server.MaxFileBytes <= 0 {
		c.Server.MaxFileBytes = 256 << 20 // 256 MiB
	}
	if c.Server.MaxMultipartMemory <= 0 {
		c.Server.MaxMultipartMemory = 8 << 20 // 8 MiB
	}

	if c.Log.Level == "" {
		c.Log.Level = "info"
	}

	if c.Repository.Type == "" {
		c.Repository.Type = "sql"
	}
	if c.Repository.SQL.Driver == "" {
		c.Repository.SQL.Driver = "postgres"
	}
	if c.Repository.SQL.MaxOpenConns <= 0 {
		c.Repository.SQL.MaxOpenConns = 20
	}
	if c.Repository.SQL.MaxIdleConns <= 0 {
		c.Repository.SQL.MaxIdleConns = 5
	}
	if c.Repository.SQL.ConnMaxLifetime <= 0 {
		c.Repository.SQL.ConnMaxLifetime = Duration(30 * time.Minute)
	}
	if c.Repository.SQL.ConnMaxIdleTime <= 0 {
		c.Repository.SQL.ConnMaxIdleTime = Duration(5 * time.Minute)
	}

	if c.FileStorage.Type == "" {
		c.FileStorage.Type = "s3"
	}
	if c.FileStorage.S3.Endpoint == "" {
		c.FileStorage.S3.Endpoint = "127.0.0.1:9000"
	}
	if c.FileStorage.S3.Bucket == "" {
		c.FileStorage.S3.Bucket = "algorithm-files"
	}

	if c.Runtime.DaprBin == "" {
		c.Runtime.DaprBin = "dapr"
	}
	if c.Runtime.ReleaseCacheTTL <= 0 {
		c.Runtime.ReleaseCacheTTL = Duration(30 * time.Second)
	}
	if c.Runtime.InvokeTimeout <= 0 {
		c.Runtime.InvokeTimeout = Duration(60 * time.Second)
	}
	if c.Runtime.MaxRestarts < 0 {
		c.Runtime.MaxRestarts = 0
	}

	if c.Session.TTL <= 0 {
		c.Session.TTL = Duration(30 * time.Minute)
	}
	if c.Session.GCInterval <= 0 {
		c.Session.GCInterval = Duration(5 * time.Minute)
	}
	if c.Session.MaxPerAlgorithm < 0 {
		c.Session.MaxPerAlgorithm = 0
	}
	if c.Session.StatefulCacheTTL <= 0 {
		c.Session.StatefulCacheTTL = Duration(30 * time.Second)
	}
	return c
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	switch c.Server.Mode {
	case "debug", "release", "test":
	default:
		return fmt.Errorf("invalid server mode %q (want debug, release or test)", c.Server.Mode)
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q (want debug, info, warn or error)", c.Log.Level)
	}
	if c.Repository.Type != "sql" {
		return fmt.Errorf("repository.type must be 'sql', got %q", c.Repository.Type)
	}
	if c.Repository.SQL.Driver != "postgres" {
		return fmt.Errorf("repository.sql.driver only supports 'postgres', got %q", c.Repository.SQL.Driver)
	}
	if c.Repository.SQL.MaxIdleConns > c.Repository.SQL.MaxOpenConns {
		return fmt.Errorf("repository.sql.maxIdleConns (%d) must not exceed maxOpenConns (%d)",
			c.Repository.SQL.MaxIdleConns, c.Repository.SQL.MaxOpenConns)
	}
	if strings.TrimSpace(c.Repository.SQL.DSN) == "" {
		return fmt.Errorf("repository.sql.dsn is required: set PG_DSN in environment or .env")
	}
	if c.FileStorage.Type != "s3" {
		return fmt.Errorf("fileStorage.type must be 's3', got %q", c.FileStorage.Type)
	}
	if c.FileStorage.S3.AccessKey == "" || c.FileStorage.S3.SecretKey == "" {
		return fmt.Errorf("fileStorage.s3 accessKey/secretKey are required: set S3_ACCESS_KEY / S3_SECRET_KEY in environment or .env")
	}
	return nil
}

// envPattern matches ${VAR} and ${VAR:-default} placeholders.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandEnv expands environment variable placeholders in the configuration.
func expandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		groups := envPattern.FindStringSubmatch(m)
		if v, ok := os.LookupEnv(groups[1]); ok {
			return v
		}
		return groups[2]
	})
}

// loadDotEnv loads variables from .env (path overridable via ENV_FILE) without overwriting existing ones.
func loadDotEnv() {
	path := os.Getenv("ENV_FILE")
	if path == "" {
		path = ".env"
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if k == "" {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}
