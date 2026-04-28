// ========================================
// main.go — CORS 配置修改
// ========================================

// ★ 修改：从配置文件读取 CORS 允许的来源，而非硬编码 "*"

// ── CORS（从配置读取允许的来源）────────────────────
corsOrigins := cfg.CORS.AllowedOrigins
if len(corsOrigins) == 0 {
    corsOrigins = []string{"*"}
}

r.Use(cors.New(cors.Config{
    AllowOrigins:     corsOrigins,
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Client-Token"},
    ExposeHeaders:    []string{"Content-Length"},
    AllowCredentials: false, // AllowOrigins=* 时必须为 false
    MaxAge:           12 * time.Hour,
}))
