package config

import (
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Stream   StreamConfig   `yaml:"stream"`
	Auth     AuthConfig     `yaml:"auth"`
	CORS     CORSConfig     `yaml:"cors"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type StreamConfig struct {
	CacheDir       string `yaml:"cache_dir"`
	MaxConcurrent  int    `yaml:"max_concurrent"`
	BufferSize     int    `yaml:"buffer_size"`
	HealthCheckSec int    `yaml:"health_check_sec"`
}

type AuthConfig struct {
	Secret        string `yaml:"secret"`
	ExpireH       int    `yaml:"expire_hours"`
	AdminPassword string `yaml:"admin_password"`
}

type CORSConfig struct {
	AllowedOrigins []string `yaml:"allowed_origins"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server: ServerConfig{Port: 9527, Host: "0.0.0.0"},
		Database: DatabaseConfig{Path: "./data/tvplayer.db"},
		Stream: StreamConfig{
			CacheDir:       "./data/cache",
			MaxConcurrent:  50,
			BufferSize:     4096,
			HealthCheckSec: 30,
		},
		Auth: AuthConfig{
			Secret:        "tvplayer-secret-key-change-me",
			ExpireH:       720,
			AdminPassword: "admin123",
		},
		CORS: CORSConfig{
			AllowedOrigins: []string{"*"},
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
	if cfg.Auth.Secret == "" {
		cfg.Auth.Secret = "tvplayer-secret-key-change-me"
	}
	if cfg.Auth.ExpireH <= 0 {
		cfg.Auth.ExpireH = 720
	}

	return cfg, nil
}
