package api

import (
	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/api/handlers"
	"github.com/mediaplayer/backend/internal/middleware"
)

type Handlers struct {
	*Handler
	*ClientHandler
	PlanHandler *PlanHandler
	LogHandler  *handlers.LogHandler
}

func NewHandlers(h *Handler, ch *ClientHandler, ph *PlanHandler, lh *handlers.LogHandler) *Handlers {
	return &Handlers{Handler: h, ClientHandler: ch, PlanHandler: ph, LogHandler: lh}
}

func (hs *Handlers) RegisterRoutes(r *gin.RouterGroup) {
	// ── 通用 API（全局限流 300次/分）─────────────────
	api := r.Group("")
	api.Use(middleware.APIRateLimit())
	{
		// 客户端自服务
		api.GET("/client/me", hs.Me)
		api.POST("/client/logs", hs.UploadLog)
		api.POST("/client/playing_status", hs.UpdatePlayingStatus)

		// 频道组
		groups := api.Group("/groups")
		groups.GET("", hs.ListGroups)

		groupWrite := groups.Group("")
		groupWrite.Use(middleware.RequireAdmin())
		{
			groupWrite.POST("", hs.CreateGroup)
			groupWrite.PUT("/:id", hs.UpdateGroup)
			groupWrite.DELETE("/:id", hs.DeleteGroup)
			groupWrite.POST("/batch", hs.BatchGroup)
		}

		// 频道
		channels := api.Group("/channels")
		channels.GET("", hs.ListChannels)
		channels.GET("/:id", hs.GetChannel)

		channelWrite := channels.Group("")
		channelWrite.Use(middleware.RequireAdmin())
		{
			channelWrite.POST("", hs.CreateChannel)
			channelWrite.PUT("/:id", hs.UpdateChannel)
			channelWrite.DELETE("/:id", hs.DeleteChannel)
			channelWrite.DELETE("/batch", hs.BatchChannel)
			channelWrite.PUT("/batch", hs.BatchUpdateChannel)
			channelWrite.POST("/mirror", hs.MirrorChannel)
			channelWrite.POST("/health-check/start", hs.TriggerHealthCheck)
			channelWrite.GET("/health-check/status", hs.GetHealthCheckStatus)
		}

		// M3U 源 (仅限管理员)
		m3u := api.Group("/m3u")
		m3u.Use(middleware.RequireAdmin())
		{
			m3u.GET("", hs.ListM3USources)
			m3u.POST("", hs.AddM3USource)
			m3u.POST("/:id/import", hs.ImportM3U)
			m3u.POST("/import-string", hs.ImportM3UString)
			m3u.POST("/format", hs.FormatSourceString)
			m3u.PUT("/:id", hs.UpdateM3USource)
			m3u.DELETE("/:id", hs.DeleteM3USource)
		}

		// 历史 & EPG & 版本 & 设置
		api.GET("/history", hs.GetHistory)
		api.POST("/history", hs.AddHistory)
		api.GET("/epg", hs.GetEPG)
		api.GET("/version", hs.GetVersion)
		api.GET("/settings", hs.GetSettings)

		// 流媒体管理 (admin 权限)
		streamWrite := api.Group("/stream")
		streamWrite.Use(middleware.RequireAdmin())
		{
			streamWrite.GET("/active", hs.GetActiveStreams)
			streamWrite.DELETE("/active/:id", hs.KillStream)
		}

		// 管理端专属
		adminRoot := api.Group("")
		adminRoot.Use(middleware.RequireAdmin())
		{
			adminRoot.POST("/settings", hs.SetSetting)
			adminRoot.GET("/stats", hs.Handler.GetStats)

			adminLogs := adminRoot.Group("/admin/logs")
			{
				adminLogs.GET("/backend", hs.LogHandler.GetBackendLogs)
				adminLogs.GET("/backend/export", hs.LogHandler.ExportBackendLogs)
				adminLogs.GET("/clients", hs.LogHandler.ListClientLogs)
				adminLogs.GET("/clients/:id", hs.LogHandler.GetClientLog)
				adminLogs.GET("/clients/:id/export", hs.LogHandler.ExportClientLog)
				adminLogs.DELETE("/clients/:id", hs.LogHandler.DeleteClientLog)
			}
		}

		// 管理端：客户端管理
		clients := api.Group("/admin/clients")
		clients.Use(middleware.RequireAdmin())
		{
			clients.GET("", hs.List)
			clients.GET("/stats", hs.ClientHandler.GetStats)
			clients.GET("/logs", hs.GetRecentLogs)
			clients.GET("/:id", hs.Get)
			clients.POST("/:id/approve", hs.Approve)
			clients.POST("/:id/reject", hs.Reject)
			clients.POST("/:id/ban", hs.Ban)
			clients.POST("/:id/unban", hs.Unban)
			clients.POST("/:id/revoke", hs.RevokeToken)
			clients.POST("/:id/regenerate", hs.RegenerateToken)
			clients.GET("/:id/logs", hs.GetLogs)
			clients.POST("/:id/log-config", hs.UpdateLogConfig)
			clients.GET("/:id/download-log", hs.DownloadLog)
			clients.POST("/:id/tester", hs.SetTester)
			clients.POST("/:id/remark", hs.UpdateRemark)
			clients.DELETE("/:id", hs.Delete)
			clients.POST("/batch", hs.Batch)
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
			adminGroups.GET("", hs.AdminListGroups)
			adminGroups.PUT("/sort", hs.BatchSortGroups)
		}

		// 管理端：频道排序
		adminChannels := api.Group("/admin/channels")
		adminChannels.Use(middleware.RequireAdmin())
		{
			adminChannels.PUT("/sort", hs.BatchSortChannels)
			adminChannels.GET("/sources", hs.GetChannelSources)
		}

		// 管理端：EPG 管理
		adminEpg := api.Group("/admin/epg")
		adminEpg.Use(middleware.RequireAdmin())
		{
			adminEpg.POST("/refresh", hs.RefreshEPG)
		}

		// 管理端：系统设置
		adminSettings := api.Group("/admin/settings")
		adminSettings.Use(middleware.RequireAdmin())
		{
			adminSettings.POST("/update", hs.SetAppUpdate)
			adminSettings.POST("/pull-update", hs.PullAppUpdate)
			adminSettings.GET("/pull-update/progress", hs.PullAppUpdateProgress)
			adminSettings.POST("/pull-update/cancel", hs.CancelPullAppUpdate)
			adminSettings.PUT("/password", hs.UpdateAdminPassword)
		}

		// 管理端：系统同步
		// Note: db_snapshot can be called by external standby nodes, so it uses its own auth inside the handler, NOT RequireAdmin.
		api.GET("/admin/system/db_snapshot", hs.GetDBSnapshot)
		api.GET("/admin/system/logos_snapshot", hs.GetLogosSnapshot)

		adminSystem := api.Group("/admin/system")
		adminSystem.Use(middleware.RequireAdmin())
		{
			adminSystem.POST("/sync_from_master", hs.SyncFromMaster)
			adminSystem.POST("/ping-master", hs.PingMaster)
		}

		// 管理端：台标管理
		adminLogo := api.Group("/admin/logo")
		adminLogo.Use(middleware.RequireAdmin())
		{
			adminLogo.POST("/cache", hs.TriggerCacheLogos)
			adminLogo.POST("/fetch", hs.TriggerBatchFetchLogos)
		}
	}

	// ── 台标接口（独立限流 600次/分）────────────────
	logo := r.Group("")
	logo.Use(middleware.LogoRateLimit())
	{
		logo.GET("/logo", hs.GetLogo)
	}

	// ── 流代理接口（独立限流 60次/分）───────────────
	stream := r.Group("/stream")
	stream.Use(middleware.StreamRateLimit())
	{
		stream.GET("/proxy/:id", hs.ProxyStream)
		stream.GET("/proxy/:id/*path", hs.ProxyStream)
		stream.GET("/catchup/:id", hs.CatchupStream)
		stream.GET("/check/:id", hs.CheckStream)
	}
}
