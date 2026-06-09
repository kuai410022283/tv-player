package config

import (
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Stream    StreamConfig    `yaml:"stream"`
	Auth      AuthConfig      `yaml:"auth"`
	CORS      CORSConfig      `yaml:"cors"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type StreamConfig struct {
	CacheDir      string `yaml:"cache_dir"`
	MaxConcurrent int    `yaml:"max_concurrent"`
	BufferSize    int    `yaml:"buffer_size"`
}

type AuthConfig struct {
	Secret        string `yaml:"secret"`
	ExpireH       int    `yaml:"expire_hours"`
	AdminPassword string `yaml:"admin_password"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

type RateLimitConfig struct {
	API    int `yaml:"api"`    // 通用 API 次/分钟/IP
	Logo   int `yaml:"logo"`   // 台标 次/分钟/IP
	Stream int `yaml:"stream"` // 流代理 次/分钟/IP
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server:   ServerConfig{Port: 9527, Host: "0.0.0.0"},
		Database: DatabaseConfig{Path: "./data/mediaplayer.db"},
		Stream: StreamConfig{
			CacheDir:      "./data/cache",
			MaxConcurrent: 50,
			BufferSize:    4096,
		},
		Auth: AuthConfig{
			Secret:        "",
			ExpireH:       720,
			AdminPassword: "",
		},
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
		},
		RateLimit: RateLimitConfig{
			API:    300, // 通用 API 300 次/分钟
			Logo:   600, // 台标 600 次/分钟
			Stream: 60,  // 流代理 60 次/分钟
		},
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("config read failed", "path", path, "error", err)
		}
		return cfg, nil
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		slog.Warn("config parse failed", "path", path, "error", err)
		return cfg, err
	}

	// 校验必填项
	if cfg.Auth.ExpireH <= 0 {
		cfg.Auth.ExpireH = 720
	}

	return cfg, nil
}
