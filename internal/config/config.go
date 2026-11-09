package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Log         LogConfig         `yaml:"log"`
	Repository  RepositoryConfig  `yaml:"repository"`
	FileStorage FileStorageConfig `yaml:"fileStorage"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// RepositoryConfig 仓储配置：当前使用关系型数据库（PostgreSQL）直连实现
type RepositoryConfig struct {
	Type string    `yaml:"type"` // 固定 "sql"
	SQL  SQLConfig `yaml:"sql"`  // type=sql 时使用
}

// SQLConfig 关系型数据库配置（直连 PostgreSQL）
type SQLConfig struct {
	Driver string `yaml:"driver"` // 仅支持 "postgres"
	DSN    string `yaml:"dsn"`    // PostgreSQL 连接串，如 host=127.0.0.1 port=54329 user=postgres password=xxx dbname=xxx sslmode=disable
}

// FileStorageConfig 文件存储配置：S3 兼容对象存储（当前后端 RustFS，可平滑替换为任意 S3 服务）
type FileStorageConfig struct {
	Type string   `yaml:"type"` // 固定 "s3"
	S3   S3Config `yaml:"s3"`
}

// S3Config S3 兼容对象存储配置（RustFS / MinIO / SeaweedFS 等通用）
type S3Config struct {
	Endpoint  string `yaml:"endpoint"`  // S3 API 地址，如 127.0.0.1:9000
	AccessKey string `yaml:"accessKey"` // 访问 Key（需与后端启动凭据一致）
	SecretKey string `yaml:"secretKey"` // 访问 Secret
	UseSSL    bool   `yaml:"useSSL"`    // 是否启用 HTTPS
	Bucket    string `yaml:"bucket"`    // 桶名，默认 algorithm-files
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s failed: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config failed: %w", err)
	}

	if err := cfg.applyDefaults().validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

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
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Repository.Type == "" {
		c.Repository.Type = "sql"
	}
	if c.Repository.SQL.Driver == "" {
		c.Repository.SQL.Driver = "postgres"
	}
	if c.Repository.SQL.DSN == "" {
		c.Repository.SQL.DSN = "host=127.0.0.1 port=54329 user=postgres password=123456 dbname=dapr sslmode=disable"
	}
	if c.FileStorage.Type == "" {
		c.FileStorage.Type = "s3"
	}
	if c.FileStorage.S3.Endpoint == "" {
		c.FileStorage.S3.Endpoint = "127.0.0.1:9000"
	}
	if c.FileStorage.S3.AccessKey == "" {
		c.FileStorage.S3.AccessKey = "minioadmin"
	}
	if c.FileStorage.S3.SecretKey == "" {
		c.FileStorage.S3.SecretKey = "minioadmin"
	}
	if c.FileStorage.S3.Bucket == "" {
		c.FileStorage.S3.Bucket = "algorithm-files"
	}
	return c
}

func (c *Config) validate() error {
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Repository.Type != "sql" {
		return fmt.Errorf("repository.type must be 'sql', got %q", c.Repository.Type)
	}
	if c.Repository.SQL.Driver != "postgres" {
		return fmt.Errorf("repository.sql.driver only supports 'postgres', got %q", c.Repository.SQL.Driver)
	}
	if c.FileStorage.Type != "s3" {
		return fmt.Errorf("fileStorage.type must be 's3', got %q", c.FileStorage.Type)
	}
	return nil
}
