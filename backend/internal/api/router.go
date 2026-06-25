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
	// ── 通用 API（全局限流 300次/分）─────────────────
	api := r.Group("")
	api.Use(middleware.APIRateLimit())
	{
		// 客户端自服务
		api.GET("/client/me", hs.ClientHandler.Me)
		api.POST("/client/logs", hs.ClientHandler.UploadLog)

		// 频道组
		groups := api.Group("/groups")
		groups.GET("", hs.Handler.ListGroups)

		groupWrite := groups.Group("")
		groupWrite.Use(middleware.RequireAdmin())
		{
			groupWrite.POST("", hs.Handler.CreateGroup)
			groupWrite.PUT("/:id", hs.Handler.UpdateGroup)
			groupWrite.DELETE("/:id", hs.Handler.DeleteGroup)
			groupWrite.POST("/batch", hs.Handler.BatchGroup)
		}

		// 频道
		channels := api.Group("/channels")
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

		// M3U 源 (仅限管理员)
		m3u := api.Group("/m3u")
		m3u.Use(middleware.RequireAdmin())
		{
			m3u.GET("", hs.Handler.ListM3USources)
			m3u.POST("", hs.Handler.AddM3USource)
			m3u.POST("/:id/import", hs.Handler.ImportM3U)
			m3u.POST("/import-string", hs.Handler.ImportM3UString)
			m3u.PUT("/:id", hs.Handler.UpdateM3USource)
			m3u.DELETE("/:id", hs.Handler.DeleteM3USource)
		}

		// 历史 & EPG & 版本 & 设置
		api.GET("/history", hs.Handler.GetHistory)
		api.POST("/history", hs.Handler.AddHistory)
		api.GET("/epg", hs.Handler.GetEPG)
		api.GET("/version", hs.Handler.GetVersion)
		api.GET("/settings", hs.Handler.GetSettings)

		// 流媒体管理 (admin 权限)
		streamWrite := api.Group("/stream")
		streamWrite.Use(middleware.RequireAdmin())
		{
			streamWrite.GET("/active", hs.Handler.GetActiveStreams)
			streamWrite.DELETE("/active/:id", hs.Handler.KillStream)
		}

		// 管理端专属
		adminRoot := api.Group("")
		adminRoot.Use(middleware.RequireAdmin())
		{
			adminRoot.POST("/settings", hs.Handler.SetSetting)
			adminRoot.GET("/stats", hs.Handler.GetStats)
		}

		// 管理端：客户端管理
		clients := api.Group("/admin/clients")
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

		// 管理端：套餐管理
		plans := api.Group("/admin/plans")
		plans.Use(middleware.RequireAdmin())
		{
			plans.GET("", hs.PlanHandler.GetPlans)
			plans.POST("", hs.PlanHandler.AddPlan)
			plans.PUT("/:id", hs.PlanHandler.UpdatePlan)
			plans.DELETE("/:id", hs.PlanHandler.DeletePlan)
		}

		// 管理端：分组管理
		adminGroups := api.Group("/admin/groups")
		adminGroups.Use(middleware.RequireAdmin())
		{
			adminGroups.GET("", hs.Handler.AdminListGroups)
		}

		// 管理端：EPG 管理
		adminEpg := api.Group("/admin/epg")
		adminEpg.Use(middleware.RequireAdmin())
		{
			adminEpg.POST("/refresh", hs.Handler.RefreshEPG)
		}

		// 管理端：系统设置
		adminSettings := api.Group("/admin/settings")
		adminSettings.Use(middleware.RequireAdmin())
		{
			adminSettings.POST("/update", hs.Handler.SetAppUpdate)
			adminSettings.POST("/pull-update", hs.Handler.PullAppUpdate)
			adminSettings.GET("/pull-update/progress", hs.Handler.PullAppUpdateProgress)
			adminSettings.PUT("/password", hs.Handler.UpdateAdminPassword)
		}

		// 管理端：系统同步
		// Note: db_snapshot can be called by external standby nodes, so it uses its own auth inside the handler, NOT RequireAdmin.
		api.GET("/admin/system/db_snapshot", hs.Handler.GetDBSnapshot)
		api.GET("/admin/system/logos_snapshot", hs.Handler.GetLogosSnapshot)

		adminSystem := api.Group("/admin/system")
		adminSystem.Use(middleware.RequireAdmin())
		{
			adminSystem.POST("/sync_from_master", hs.Handler.SyncFromMaster)
			adminSystem.POST("/ping-master", hs.Handler.PingMaster)
		}

		// 管理端：台标管理
		adminLogo := api.Group("/admin/logo")
		adminLogo.Use(middleware.RequireAdmin())
		{
			adminLogo.POST("/cache", hs.Handler.TriggerCacheLogos)
			adminLogo.POST("/fetch", hs.Handler.TriggerBatchFetchLogos)
		}
	}

	// ── 台标接口（独立限流 600次/分）────────────────
	logo := r.Group("")
	logo.Use(middleware.LogoRateLimit())
	{
		logo.GET("/logo", hs.Handler.GetLogo)
	}

	// ── 流代理接口（独立限流 60次/分）───────────────
	stream := r.Group("/stream")
	stream.Use(middleware.StreamRateLimit())
	{
		stream.GET("/proxy/:id", hs.Handler.ProxyStream)
		stream.GET("/proxy/:id/*path", hs.Handler.ProxyStream)
		stream.GET("/catchup/:id", hs.Handler.CatchupStream)
		stream.GET("/check/:id", hs.Handler.CheckStream)
	}
}