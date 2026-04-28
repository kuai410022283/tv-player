// ========================================
// main.go — 修改 checkSecurityDefaults 函数
// ========================================

// checkSecurityDefaults 检查默认凭据并在生产环境给出警告
func checkSecurityDefaults(cfg *config.Config) {
	warnings := []string{}

	if cfg.Auth.Secret == "" || cfg.Auth.Secret == "tvplayer-secret-key-change-me" || cfg.Auth.Secret == "tvplayer-change-this-secret-key" {
		warnings = append(warnings, "JWT secret 使用了默认值，请在 config.yaml 中修改 auth.secret")
	}

	// ★ 修改：检查密码是否为默认值或明文
	if cfg.Auth.AdminPassword == "" || cfg.Auth.AdminPassword == "admin123" {
		warnings = append(warnings, "管理员密码使用了默认值，请在 config.yaml 中修改 auth.admin_password")
	} else if !isBcryptHash(cfg.Auth.AdminPassword) {
		warnings = append(warnings, "管理员密码未使用 bcrypt 哈希存储，建议使用哈希存储")
	}

	if len(warnings) > 0 {
		for _, w := range warnings {
			slog.Warn("⚠️ 安全警告: " + w)
		}
		if os.Getenv("ALLOW_INSECURE_DEFAULTS") == "" {
			if os.Getenv("GIN_MODE") == "release" || os.Getenv("ENV") == "production" {
				slog.Error("检测到生产环境使用默认凭据，拒绝启动。请修改配置后重试，或设置 ALLOW_INSECURE_DEFAULTS=1 强制启动")
				os.Exit(1)
			}
		}
	}

	// 打印有效配置（不含密码）
	slog.Info("config loaded",
		"server.port", cfg.Server.Port,
		"database.path", cfg.Database.Path,
		"stream.max_concurrent", cfg.Stream.MaxConcurrent,
		"auth.expire_hours", cfg.Auth.ExpireH,
		"auth.password_hashed", isBcryptHash(cfg.Auth.AdminPassword),
	)
}
