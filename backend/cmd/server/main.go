package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/api"
	"github.com/mediaplayer/backend/internal/api/handlers"
	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/middleware"
	"github.com/mediaplayer/backend/internal/services"
	"gopkg.in/natefinch/lumberjack.v2"
)

// Version 由编译时注入: go build -ldflags "-X main.Version=v1.0.0"
var Version = "dev"

func main() {
	// 从 version 文件读取版本号
	if content, err := os.ReadFile("version"); err == nil {
		Version = strings.TrimSpace(string(content))
	}
	// ── 结构化日志初始化 ───────────────────────────────
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}

	_ = os.MkdirAll("./data/logs", 0755)
	logFile := &lumberjack.Logger{
		Filename:   "./data/logs/backend.log",
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		MaxAge:     7, //days
		Compress:   true,
	}

	mw := io.MultiWriter(os.Stdout, logFile)
	slog.SetDefault(slog.New(slog.NewJSONHandler(mw, &slog.HandlerOptions{
		Level: logLevel,
	})))

	fmt.Println("项目名称：MediaPlayer")
	fmt.Println("版本：", Version)
	fmt.Println("作者：laok")
	fmt.Println("邮箱：kuai410022283@qq.com")
	fmt.Println("TG群组: https://t.me/+3qS4i6yrHsc2MWNl")
	fmt.Println("项目地址: https://github.com/kuai410022283/mediaplayer")

	// ── 加载配置 ─────────────────────────────────────
	cfgPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Warn("config load failed, using defaults", "error", err)
	}

	// ── 启动安全检查 ─────────────────────────────────
	checkSecurityDefaults(cfg)

	// ── 初始化限流器（从配置覆盖默认值）──────────────
	middleware.InitRateLimiters(cfg.RateLimit.API, cfg.RateLimit.Logo, cfg.RateLimit.Stream)

	// ── 初始化数据库 ─────────────────────────────────
	dbPath := cfg.Database.Path
	_ = os.MkdirAll("./data", 0755)
	db, err := services.InitDB(dbPath)
	if err != nil {
		slog.Error("database init failed", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// ── 初始化服务 ───────────────────────────────────
	channelSvc := services.NewChannelService(db)
	streamProxy := services.NewStreamProxy(&cfg.Stream, channelSvc)
	importer := services.NewM3UImporter(channelSvc)
	clientSvc := services.NewClientService(db)
	epgSvc := services.NewEPGService(db)
	logoSvc := services.NewLogoService(db)
	syncSvc := services.NewSyncService(db)
	logSvc := services.NewLogService()
	clientConfigSvc := services.NewClientConfigService(db)

	// ── 读取设置并初始化本地文件开关 ────────────────
	if settings, err := channelSvc.GetAllSettings(); err == nil {
		if v, ok := settings["allow_local_file"]; ok {
			services.AllowLocalFile = v == "true" || v == "1"
		}
	}

	// ── 启动后台任务 ─────────────────────────────────
	stop := make(chan struct{})
	go startClientExpiry(clientSvc, stop)
	go middleware.StartRateLimitCleanup(stop)
	epgSvc.StartEPGScheduler()

	// ── 初始化 Gin ──────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// 信任反向代理，从 X-Forwarded-For/X-Real-IP 读取真实客户端 IP
	// 默认信任本机代理，Docker 部署需在 config.yaml 中配置 server.trusted_proxies
	trustedProxies := cfg.Server.TrustedProxies
	if len(trustedProxies) == 0 {
		trustedProxies = []string{"127.0.0.1", "::1"}
	}
	_ = r.SetTrustedProxies(trustedProxies)
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())
	r.Use(gzipMiddleware()) // 启用 gzip 压缩，优化大数据量传输

	// 初始化 JWT（传入过期小时数）
	api.InitSecret(cfg.Auth.Secret, cfg.Auth.AdminPassword, cfg.Auth.ExpireH)

	// ── CORS（限制允许的来源）────────────────────────
	corsOrigins := cfg.CORS.AllowedOrigins
	if len(corsOrigins) == 0 {
		corsOrigins = []string{"*"}
	}
	allowCreds := len(corsOrigins) == 1 && corsOrigins[0] != "*"
	r.Use(cors.New(cors.Config{
		AllowOrigins:     corsOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Client-Token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: allowCreds,
		MaxAge:           12 * time.Hour,
	}))

	// ── 安全响应头 ──────────────────────────────────
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "no-referrer")
		// CSP: 允许 inline script (管理后台需要) + 同源资源
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https: http:; connect-src 'self' https: http:")
		c.Next()
	})

	// ── 健康检查（无需认证）──────────────────────────
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
			"version": Version,
		})
	})

	planSvc := services.NewPlanService(db, logoSvc)

	// ── 初始化 Handler（所有路由共享同一实例）────────
	h := api.NewHandler(channelSvc, streamProxy, importer, clientSvc, epgSvc, logoSvc, syncSvc, Version)
	ch := api.NewClientHandler(clientSvc, channelSvc, logSvc, streamProxy, clientConfigSvc)
	ph := api.NewPlanHandler(planSvc)
	lh := handlers.NewLogHandler(logSvc)
	hs := api.NewHandlers(h, ch, ph, lh)

	// 禁用 API 缓存的中间件
	noCache := func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	}

	// ── 公开 API（无需认证，独立限流）───────────────
	public := r.Group("/api/v1")
	public.Use(noCache)
	{
		public.POST("/admin/login", middleware.LoginRateLimit(), h.AdminLogin)
		public.GET("/admin/config", h.GetAdminConfig)
		public.POST("/client/register", middleware.ClientAuthRateLimit(), ch.Register)
		public.GET("/client/verify", middleware.ClientAuthRateLimit(), ch.Verify)
		public.POST("/client/verify", middleware.ClientAuthRateLimit(), ch.Verify)
		public.GET("/update", h.GetAppUpdate)
		public.GET("/subscription", ph.GetSubscription)
	}

	// ── 受保护 API（仅认证，不限流——限流由 router.go 内部分组控制）─
	v1 := r.Group("/api/v1")
	v1.Use(noCache)
	{
		v1.Use(middleware.AuthMiddleware(cfg.Auth.Secret, db))
		hs.RegisterRoutes(v1)
	}

	_ = os.MkdirAll("./library/channel_logo", 0755)
	r.Static("/library/channel_logo", "./library/channel_logo")

	// 开机广告支持
	_ = os.MkdirAll("./library/ad", 0755)
	r.Static("/ad", "./library/ad")
	r.Static("/static", "./web/static")
	r.Static("/admin", "./web/admin")
	r.Static("/download", "./web/download")
	r.StaticFile("/", "./web/index.html")

	// ── 启动后台任务 ────────────────────────────────
	importer.StartAutoSync()
	syncSvc.StartSyncCron(channelSvc)

	// ── 启动服务 ────────────────────────────────────
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	slog.Info("MediaPlayer starting",
		"addr", addr,
		"version", Version,
		"admin_panel", fmt.Sprintf("http://localhost:%d/admin/", cfg.Server.Port),
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	// ── 优雅关闭 ────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("shutting down...", "signal", sig.String())
	close(stop)

	// 等待进行中的请求完成（最多 10 秒）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("server forced shutdown", "error", err)
	}

	slog.Info("server exited")
}

// checkSecurityDefaults 检查默认凭据并在生产环境给出警告
func checkSecurityDefaults(cfg *config.Config) {
	warnings := []string{}

	if cfg.Auth.Secret == "" || cfg.Auth.Secret == "mediaplayer-secret-key-change-me" || cfg.Auth.Secret == "mediaplayer-change-this-secret-key" {
		warnings = append(warnings, "JWT secret 使用了默认值，请在 config.yaml 中修改 auth.secret")
	}

	if cfg.Auth.AdminPassword == "" || cfg.Auth.AdminPassword == "admin123" {
		warnings = append(warnings, "管理员密码使用了默认值或为空，请在 config.yaml 中修改 auth.admin_password")
	}

	if len(warnings) > 0 {
		for _, w := range warnings {
			slog.Warn("⚠️ 安全警告: " + w)
		}
		if os.Getenv("ALLOW_INSECURE_DEFAULTS") == "" {
			// 生产环境：默认凭据必须修改，除非设置 ALLOW_INSECURE_DEFAULTS=1
			// 开发环境可以设置该变量跳过
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
	)
}

// startClientExpiry 定时清理过期客户端
func startClientExpiry(clientSvc *services.ClientService, stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n, _ := clientSvc.ExpireOldClients()
			if n > 0 {
				slog.Info("expired clients cleaned", "count", n)
			}
		}
	}
}

// gzipMiddleware 返回一个简单的 gzip 压缩中间件
func gzipMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查客户端是否支持 gzip
		if !strings.Contains(c.GetHeader("Accept-Encoding"), "gzip") {
			c.Next()
			return
		}

		// 跳过流媒体代理和文件下载
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/v1/stream") ||
			strings.HasPrefix(path, "/library") ||
			strings.HasPrefix(path, "/download") {
			c.Next()
			return
		}

		// 设置 gzip writer
		c.Header("Content-Encoding", "gzip")
		c.Header("Vary", "Accept-Encoding")

		gz := gzip.NewWriter(c.Writer)
		defer func() { _ = gz.Close() }()

		c.Writer = &gzipResponseWriter{ResponseWriter: c.Writer, Writer: gz}
		c.Next()
	}
}

type gzipResponseWriter struct {
	gin.ResponseWriter
	Writer io.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}
