package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/tvplayer/backend/internal/api"
	"github.com/tvplayer/backend/internal/config"
	"github.com/tvplayer/backend/internal/middleware"
	"github.com/tvplayer/backend/internal/services"
)

// Version 由编译时注入: go build -ldflags "-X main.Version=v1.0.0"
var Version = "dev"

func main() {
	// ── 结构化日志初始化 ───────────────────────────────
	logLevel := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		logLevel = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})))

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

	// ── 初始化数据库 ─────────────────────────────────
	dbPath := cfg.Database.Path
	os.MkdirAll("./data", 0755)
	db, err := services.InitDB(dbPath)
	if err != nil {
		slog.Error("database init failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// ── 初始化服务 ───────────────────────────────────
	channelSvc := services.NewChannelService(db)
	streamProxy := services.NewStreamProxy(&cfg.Stream, channelSvc)
	importer := services.NewM3UImporter(channelSvc)
	clientSvc := services.NewClientService(db)

	// ── 启动后台任务 ─────────────────────────────────
	stop := make(chan struct{})
	go streamProxy.StartHealthCheck(stop)
	go startClientExpiry(clientSvc, stop)
	go middleware.StartRateLimitCleanup(stop)

	// ── 初始化 Gin ──────────────────────────────────
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())

	// 初始化 JWT（传入过期小时数）
	api.InitSecret(cfg.Auth.Secret, cfg.Auth.AdminPassword, cfg.Auth.ExpireH)

	// ── CORS（限制允许的来源）────────────────────────
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Client-Token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false, // AllowOrigins=* 时必须为 false
		MaxAge:           12 * time.Hour,
	}))

	// ── 安全响应头 ──────────────────────────────────
	r.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	})

	// ── 健康检查（无需认证）──────────────────────────
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
			"version": Version,
		})
	})

	// ── 初始化 Handler（所有路由共享同一实例）────────
	h := api.NewHandler(channelSvc, streamProxy, importer, clientSvc)
	ch := api.NewClientHandler(clientSvc)
	hs := api.NewHandlers(h, ch)

	// ── 公开 API（无需认证，独立限流）───────────────
	public := r.Group("/api/v1")
	{
		public.POST("/admin/login", middleware.LoginRateLimit(), h.AdminLogin)
		public.POST("/client/register", ch.Register)
		public.GET("/client/verify", ch.Verify)
		public.POST("/client/verify", ch.Verify)
	}

	// ── 受保护 API（全局限流 + 认证）───────────────
	v1 := r.Group("/api/v1")
	{
		v1.Use(middleware.APIRateLimit())
		v1.Use(middleware.AuthMiddleware(cfg.Auth.Secret, db))
		hs.RegisterRoutes(v1)
	}

	// ── 静态文件（管理后台）──────────────────────────
	r.Static("/admin", "./web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(302, "/admin")
	})

	// ── 启动服务 ────────────────────────────────────
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	slog.Info("TVPlayer starting",
		"addr", addr,
		"version", Version,
		"admin_panel", fmt.Sprintf("http://localhost:%d/admin/", cfg.Server.Port),
	)

	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
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

	if cfg.Auth.Secret == "" || cfg.Auth.Secret == "tvplayer-secret-key-change-me" || cfg.Auth.Secret == "tvplayer-change-this-secret-key" {
		warnings = append(warnings, "JWT secret 使用了默认值，请在 config.yaml 中修改 auth.secret")
	}

	if cfg.Auth.AdminPassword == "" || cfg.Auth.AdminPassword == "admin123" {
		warnings = append(warnings, "管理员密码使用了默认值，请在 config.yaml 中修改 auth.admin_password")
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

// 用于 admin login 的路由注册，避免与 router.go 中重复
func init() {
	// 抑制未使用导入的编译错误
	_ = strings.Contains
}
