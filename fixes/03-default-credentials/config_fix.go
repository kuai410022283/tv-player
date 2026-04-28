// ========================================
// config.go — 修改 Load 函数，移除不安全的默认值
// ========================================

package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	// ★ 修改：不再硬编码默认密码和 secret
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
			Secret:        "", // 空值，下面会检测并提示
			ExpireH:       720,
			AdminPassword: "", // 空值，下面会检测并提示
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

	// ★ 新增：自动生成缺失的 secret
	if cfg.Auth.Secret == "" || cfg.Auth.Secret == "tvplayer-secret-key-change-me" || cfg.Auth.Secret == "tvplayer-change-this-secret-key" {
		newSecret, err := generateRandomSecret(32)
		if err != nil {
			slog.Error("无法生成随机 secret", "error", err)
			cfg.Auth.Secret = "tvplayer-secret-key-change-me"
		} else {
			cfg.Auth.Secret = newSecret
			slog.Info("已自动生成 JWT secret（重启后会变化，建议写入配置文件）")
			// 提示用户将生成的 secret 写入配置文件
			slog.Info(fmt.Sprintf("建议将以下内容写入 %s: auth.secret: \"%s\"", path, newSecret))
		}
	}

	// 校验必填项
	if cfg.Auth.ExpireH <= 0 {
		cfg.Auth.ExpireH = 720
	}

	// ★ 新增：检查管理员密码是否设置
	if cfg.Auth.AdminPassword == "" {
		slog.Error("═══════════════════════════════════════════════════════════")
		slog.Error("⚠️  管理员密码未设置！")
		slog.Error("请在 config.yaml 中设置 auth.admin_password")
		slog.Error("生成哈希密码: go run cmd/hash-password/main.go \"你的密码\"")
		slog.Error("═══════════════════════════════════════════════════════════")
	}

	return cfg, nil
}

// generateRandomSecret 生成指定长度的随机十六进制字符串
func generateRandomSecret(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
