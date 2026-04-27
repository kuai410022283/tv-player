package main

import (
	"fmt"
	"log"
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

func main() {
	// Load config
	cfgPath := "config.yaml"
	if p := os.Getenv("CONFIG_PATH"); p != "" {
		cfgPath = p
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("Warning: %v, using defaults", err)
	}

	// Init database
	dbPath := cfg.Database.Path
	os.MkdirAll("./data", 0755)
	db, err := services.InitDB(dbPath)
	if err != nil {
		log.Fatalf("Database init failed: %v", err)
	}
	defer db.Close()

	// Init services
	channelSvc := services.NewChannelService(db)
	streamProxy := services.NewStreamProxy(&cfg.Stream, channelSvc)
	importer := services.NewM3UImporter(channelSvc)
	clientSvc := services.NewClientService(db)

	// Start health check
	stop := make(chan struct{})
	go streamProxy.StartHealthCheck(stop)

	// 定时清理过期客户端 (每小时)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				n, _ := clientSvc.ExpireOldClients()
				if n > 0 {
					log.Printf("⏰ 已将 %d 个过期客户端标记为 expired", n)
				}
			}
		}
	}()

	// Init Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())

	// CORS
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Client-Token"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	// Health check
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"message": "pong"})
	})

	// API v1
	h := api.NewHandler(channelSvc, streamProxy, importer)
	ch := api.NewClientHandler(clientSvc)
	hs := api.NewHandlers(h, ch)

	v1 := r.Group("/api/v1")
	{
		// 认证中间件 (支持 admin JWT + client token)
		v1.Use(middleware.AuthMiddleware(cfg.Auth.Secret, db))
		hs.RegisterRoutes(v1)
	}

	// Serve static files (admin panel)
	r.Static("/admin", "./web")
	r.GET("/", func(c *gin.Context) {
		c.Redirect(302, "/admin")
	})

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("🚀 TVPlayer Backend starting on %s", addr)
	log.Printf("📡 Admin panel: http://localhost:%d/admin/", cfg.Server.Port)
	log.Printf("🔑 Client register: POST /api/v1/client/register")
	log.Printf("✅ Client verify:   GET /api/v1/client/verify")

	// Graceful shutdown
	go func() {
		if err := r.Run(addr); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down...")
	close(stop)
}
