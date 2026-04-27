package api

import (
	"github.com/gin-gonic/gin"
	"github.com/tvplayer/backend/internal/middleware"
)

type Handlers struct {
	*Handler
	*ClientHandler
}

func NewHandlers(h *Handler, ch *ClientHandler) *Handlers {
	return &Handlers{Handler: h, ClientHandler: ch}
}

func (hs *Handlers) RegisterRoutes(r *gin.RouterGroup) {
	// ── 客户端公开接口 (无需认证) ────────────────────
	r.POST("/client/register", hs.ClientHandler.Register)
	r.GET("/client/verify", hs.ClientHandler.Verify)
	r.POST("/client/verify", hs.ClientHandler.Verify)

	// ── 管理员登录 (无需认证) ─────────────────────
	r.POST("/admin/login", hs.Handler.AdminLogin)

	// ── Admin 登录 (公开) ───────────────────────────
	r.POST("/admin/login", hs.Handler.AdminLogin)

	// ── 客户端自服务 (需要客户端 token) ──────────────
	r.GET("/client/me", hs.ClientHandler.Me)

	// ── 频道组 ──────────────────────────────────────
	groups := r.Group("/groups")
	{
		groups.GET("", hs.Handler.ListGroups)
		groups.POST("", hs.Handler.CreateGroup)
		groups.PUT("/:id", hs.Handler.UpdateGroup)
		groups.DELETE("/:id", hs.Handler.DeleteGroup)
	}

	// ── 频道 ────────────────────────────────────────
	channels := r.Group("/channels")
	{
		channels.GET("", hs.Handler.ListChannels)
		channels.GET("/:id", hs.Handler.GetChannel)
		channels.POST("", hs.Handler.CreateChannel)
		channels.PUT("/:id", hs.Handler.UpdateChannel)
		channels.DELETE("/:id", hs.Handler.DeleteChannel)
		channels.POST("/:id/favorite", hs.Handler.ToggleFavorite)
	}

	// ── 流媒体 ──────────────────────────────────────
	stream := r.Group("/stream")
	{
		stream.GET("/proxy/:id", hs.Handler.ProxyStream)
		stream.GET("/check/:id", hs.Handler.CheckStream)
		stream.GET("/active", hs.Handler.GetActiveStreams)
	}

	// ── M3U 源 ──────────────────────────────────────
	m3u := r.Group("/m3u")
	{
		m3u.GET("", hs.Handler.ListM3USources)
		m3u.POST("", hs.Handler.AddM3USource)
		m3u.POST("/:id/import", hs.Handler.ImportM3U)
		m3u.POST("/import-string", hs.Handler.ImportM3UString)
		m3u.DELETE("/:id", hs.Handler.DeleteM3USource)
	}

	// ── 历史 & 设置 & 统计 & EPG ─────────────────────
	r.GET("/history", hs.Handler.GetHistory)
	r.POST("/history", hs.Handler.AddHistory)
	r.GET("/settings", hs.Handler.GetSettings)
	r.POST("/settings", hs.Handler.SetSetting)
	r.GET("/stats", hs.Handler.GetStats)
	r.GET("/epg", hs.Handler.GetEPG)

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
}
