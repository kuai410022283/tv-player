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

	// ── 频道组 ──────────────────────────────────────
	groups := r.Group("/groups")
	{
		groups.GET("", hs.Handler.ListGroups)
		groups.POST("", hs.Handler.CreateGroup)
		groups.PUT("/:id", hs.Handler.UpdateGroup)
		groups.DELETE("/:id", hs.Handler.DeleteGroup)
		groups.POST("/batch", hs.Handler.BatchGroup)
	}

	// ── 频道 ────────────────────────────────────────
	channels := r.Group("/channels")
	{
		channels.GET("", hs.Handler.ListChannels)
		channels.GET("/:id", hs.Handler.GetChannel)
		channels.POST("", hs.Handler.CreateChannel)
		channels.PUT("/:id", hs.Handler.UpdateChannel)
		channels.DELETE("/:id", hs.Handler.DeleteChannel)
		channels.DELETE("/batch", hs.Handler.BatchChannel)
	}

	// ── 流媒体 ──────────────────────────────────────
	stream := r.Group("/stream")
	{
		stream.GET("/proxy/:id", hs.Handler.ProxyStream)
		stream.GET("/proxy/:id/*path", hs.Handler.ProxyStream)
		stream.GET("/catchup/:id", hs.Handler.CatchupStream)
		stream.GET("/check/:id", hs.Handler.CheckStream)
		stream.GET("/active", hs.Handler.GetActiveStreams)
		stream.DELETE("/active/:id", hs.Handler.KillStream)
	}

	// ── M3U 源 ──────────────────────────────────────
	m3u := r.Group("/m3u")
	{
		m3u.GET("", hs.Handler.ListM3USources)
		m3u.POST("", hs.Handler.AddM3USource)
		m3u.POST("/:id/import", hs.Handler.ImportM3U)
		m3u.POST("/import-string", hs.Handler.ImportM3UString)
		m3u.PUT("/:id", hs.Handler.UpdateM3USource)
		m3u.DELETE("/:id", hs.Handler.DeleteM3USource)
	}

	// ── 历史 & 设置 & 统计 & EPG & 版本 ─────────────────
	r.GET("/history", hs.Handler.GetHistory)
	r.POST("/history", hs.Handler.AddHistory)
	r.GET("/settings", hs.Handler.GetSettings)
	r.POST("/settings", hs.Handler.SetSetting)
	r.GET("/stats", hs.Handler.GetStats)
	r.GET("/epg", hs.Handler.GetEPG)
	r.GET("/version", hs.Handler.GetVersion)

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
}
