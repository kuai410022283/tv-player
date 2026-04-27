package config

import (
	"os"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Stream   StreamConfig   `yaml:"stream"`
	Auth     AuthConfig     `yaml:"auth"`
}

type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type StreamConfig struct {
	CacheDir        string `yaml:"cache_dir"`
	MaxConcurrent   int    `yaml:"max_concurrent"`
	BufferSize      int    `yaml:"buffer_size"`
	HealthCheckSec  int    `yaml:"health_check_sec"`
}

type AuthConfig struct {
	Secret   string `yaml:"secret"`
	ExpireH  int    `yaml:"expire_hours"`
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
		Auth: AuthConfig{Secret: "tvplayer-secret-key-change-me", ExpireH: 720},
	}

	data, err := os.ReadFile(path)
	if err == nil {
		yaml.Unmarshal(data, cfg)
	}
	return cfg, nil
}
