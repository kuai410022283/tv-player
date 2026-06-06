package api

import (
	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/middleware"
)

type Handlers struct {
	*Handler
	*ClientHandler
	PlanHandler *PlanHandler
}

func NewHandlers(h *Handler, ch *ClientHandler, ph *PlanHandler) *Handlers {
	return &Handlers{Handler: h, ClientHandler: ch, PlanHandler: ph}
}

func (hs *Handlers) RegisterRoutes(r *gin.RouterGroup) {
	// ── 客户端公开接口 (已在 main.go 中注册，带限流) ───
	// r.POST("/client/register", ...) — 已移至 main.go
	// r.GET("/client/verify", ...) — 已移至 main.go
	// r.POST("/client/verify", ...) — 已移至 main.go
	// r.POST("/admin/login", ...) — 已移至 main.go

	// ── 客户端自服务 (需要客户端 token) ──────────────
	r.GET("/client/me", hs.ClientHandler.Me)
	r.POST("/client/logs", hs.ClientHandler.UploadLog)

	// ── 频道组 ──────────────────────────────────────
	groups := r.Group("/groups")
	groups.GET("", hs.Handler.ListGroups)
	
	groupWrite := groups.Group("")
	groupWrite.Use(middleware.RequireAdmin())
	{
		groupWrite.POST("", hs.Handler.CreateGroup)
		groupWrite.PUT("/:id", hs.Handler.UpdateGroup)
		groupWrite.DELETE("/:id", hs.Handler.DeleteGroup)
		groupWrite.POST("/batch", hs.Handler.BatchGroup)
	}

	// ── 频道 ────────────────────────────────────────
	channels := r.Group("/channels")
	channels.GET("", hs.Handler.ListChannels)
	channels.GET("/:id", hs.Handler.GetChannel)
	
	channelWrite := channels.Group("")
	channelWrite.Use(middleware.RequireAdmin())
	{
		channelWrite.POST("", hs.Handler.CreateChannel)
		channelWrite.PUT("/:id", hs.Handler.UpdateChannel)
		channelWrite.DELETE("/:id", hs.Handler.DeleteChannel)
		channelWrite.DELETE("/batch", hs.Handler.BatchChannel)
		channelWrite.POST("/health-check/start", hs.Handler.TriggerHealthCheck)
		channelWrite.GET("/health-check/status", hs.Handler.GetHealthCheckStatus)
	}

	// ── 流媒体 ──────────────────────────────────────
	stream := r.Group("/stream")
	stream.GET("/proxy/:id", hs.Handler.ProxyStream)
	stream.GET("/proxy/:id/*path", hs.Handler.ProxyStream)
	stream.GET("/catchup/:id", hs.Handler.CatchupStream)
	stream.GET("/check/:id", hs.Handler.CheckStream) // 测试用，可公开
	
	streamWrite := stream.Group("")
	streamWrite.Use(middleware.RequireAdmin())
	{
		streamWrite.GET("/active", hs.Handler.GetActiveStreams)
		streamWrite.DELETE("/active/:id", hs.Handler.KillStream)
	}

	// ── M3U 源 ──────────────────────────────────────
	m3u := r.Group("/m3u")
	m3u.Use(middleware.RequireAdmin()) // 仅限管理员
	{
		m3u.GET("", hs.Handler.ListM3USources)
		m3u.POST("", hs.Handler.AddM3USource)
		m3u.POST("/:id/import", hs.Handler.ImportM3U)
		m3u.POST("/import-string", hs.Handler.ImportM3UString)
		m3u.PUT("/:id", hs.Handler.UpdateM3USource)
		m3u.DELETE("/:id", hs.Handler.DeleteM3USource)
	}

	// ── 历史 & EPG & 版本 & 台标 (客户端可读写) ─────────────────
	r.GET("/history", hs.Handler.GetHistory)
	r.POST("/history", hs.Handler.AddHistory)
	r.GET("/epg", hs.Handler.GetEPG)
	r.GET("/version", hs.Handler.GetVersion)
	r.GET("/settings", hs.Handler.GetSettings) // 客户端需读取公告等
	r.GET("/logo", hs.Handler.GetLogo)         // 获取台标 (需鉴权)
	
	// ── 管理端专属 ──────────────────────────────────
	adminRoot := r.Group("")
	adminRoot.Use(middleware.RequireAdmin())
	{
		adminRoot.POST("/settings", hs.Handler.SetSetting)
		adminRoot.GET("/stats", hs.Handler.GetStats)
	}

	// ── 管理端：客户端管理 (需要 admin 权限) ────────
	clients := r.Group("/admin/clients")
	clients.Use(middleware.RequireAdmin())
	{
		clients.GET("", hs.ClientHandler.List)
		clients.GET("/stats", hs.ClientHandler.GetStats)
		clients.GET("/logs", hs.ClientHandler.GetRecentLogs)
		clients.GET("/:id", hs.ClientHandler.Get)
		clients.POST("/:id/approve", hs.ClientHandler.Approve)
		clients.POST("/:id/reject", hs.ClientHandler.Reject)
		clients.POST("/:id/ban", hs.ClientHandler.Ban)
		clients.POST("/:id/unban", hs.ClientHandler.Unban)
		clients.POST("/:id/revoke", hs.ClientHandler.RevokeToken)
		clients.POST("/:id/regenerate", hs.ClientHandler.RegenerateToken)
		clients.GET("/:id/logs", hs.ClientHandler.GetLogs)
		clients.POST("/:id/log-config", hs.ClientHandler.UpdateLogConfig)
		clients.GET("/:id/download-log", hs.ClientHandler.DownloadLog)
		clients.DELETE("/:id", hs.ClientHandler.Delete)
		clients.POST("/batch", hs.ClientHandler.Batch)
	}

	// ── 管理端：套餐管理 (需要 admin 权限) ────────────
	plans := r.Group("/admin/plans")
	plans.Use(middleware.RequireAdmin())
	{
		plans.GET("", hs.PlanHandler.GetPlans)
		plans.POST("", hs.PlanHandler.AddPlan)
		plans.PUT("/:id", hs.PlanHandler.UpdatePlan)
		plans.DELETE("/:id", hs.PlanHandler.DeletePlan)
	}

	// ── 管理端：分组管理 (带分页/搜索) ────────────
	adminGroups := r.Group("/admin/groups")
	adminGroups.Use(middleware.RequireAdmin())
	{
		adminGroups.GET("", hs.Handler.AdminListGroups)
	}

	// ── 管理端：EPG 管理 ──────────────────────────
	adminEpg := r.Group("/admin/epg")
	adminEpg.Use(middleware.RequireAdmin())
	{
		adminEpg.POST("/refresh", hs.Handler.RefreshEPG)
	}

	// ── 管理端：系统设置 ──────────────────────────
	adminSettings := r.Group("/admin/settings")
	adminSettings.Use(middleware.RequireAdmin())
	{
		adminSettings.POST("/update", hs.Handler.SetAppUpdate)
		adminSettings.PUT("/password", hs.Handler.UpdateAdminPassword)
	}

	// ── 管理端：台标管理 ──────────────────────────
	adminLogo := r.Group("/admin/logo")
	adminLogo.Use(middleware.RequireAdmin())
	{
		adminLogo.POST("/cache", hs.Handler.TriggerCacheLogos)
		adminLogo.POST("/fetch", hs.Handler.TriggerBatchFetchLogos)
	}
}
