package api

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services"
	"github.com/mediaplayer/backend/internal/utils"
)

type ClientHandler struct {
	clientSvc       *services.ClientService
	channelSvc      *services.ChannelService
	logSvc          *services.LogService
	streamProxy     *services.StreamProxy
	clientConfigSvc *services.ClientConfigService
}

func NewClientHandler(clientSvc *services.ClientService, channelSvc *services.ChannelService, logSvc *services.LogService, streamProxy *services.StreamProxy, clientConfigSvc *services.ClientConfigService) *ClientHandler {
	return &ClientHandler{clientSvc: clientSvc, channelSvc: channelSvc, logSvc: logSvc, streamProxy: streamProxy, clientConfigSvc: clientConfigSvc}
}

// ── 客户端：注册 ───────────────────────────────────────

func (h *ClientHandler) Register(c *gin.Context) {
	var req models.ClientRegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误: 设备名称和设备ID必填")
		return
	}

	ip := utils.GetRealClientIP(c)
	resp, err := h.clientSvc.Register(&req, ip)
	if err != nil {
		slog.Error("client register failed", "error", err, "device_id", req.DeviceID)
		fail(c, 500, "注册失败，请稍后重试")
		return
	}

	h.clientSvc.AddLog(resp.ClientID, "register", 0, ip, c.GetHeader("User-Agent"), "")

	serverName, _ := h.channelSvc.GetSetting("server_name")
	appDisplayName, _ := h.channelSvc.GetSetting("app_display_name")

	announcement, _ := h.channelSvc.GetSetting("system_announcement")
	announcementIntervalStr, _ := h.channelSvc.GetSetting("system_announcement_interval")
	announcementInterval, _ := strconv.Atoi(announcementIntervalStr)

	// 开机短视频/广告
	startupMediaEnabledStr, _ := h.channelSvc.GetSetting("startup_media_enabled")
	startupMediaEnabled := startupMediaEnabledStr == "true"
	startupMedia, _ := h.channelSvc.GetSetting("startup_media_url")
	startupMediaType, _ := h.channelSvc.GetSetting("startup_media_type")
	if startupMediaType == "" {
		startupMediaType = "auto"
	}
	startupDurationStr, _ := h.channelSvc.GetSetting("startup_duration")
	startupDuration, _ := strconv.Atoi(startupDurationStr)
	if startupDuration == 0 {
		startupDuration = 5 // 默认5秒防死锁
	}
	startupSkipAfterStr, _ := h.channelSvc.GetSetting("startup_skip_after")
	startupSkipAfter, _ := strconv.Atoi(startupSkipAfterStr)

	maintenanceModeStr, _ := h.channelSvc.GetSetting("maintenance_mode")
	globalMaintenance := maintenanceModeStr == "true"

	backupServersStr, _ := h.channelSvc.GetSetting("server_backup_urls")
	var backupServers []string
	if backupServersStr != "" {
		for _, s := range strings.Split(backupServersStr, "\n") {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				encoded := base64.StdEncoding.EncodeToString([]byte(trimmed))
				backupServers = append(backupServers, encoded)
			}
		}
	}

	if resp.Status == "approved" {
		ok(c, gin.H{
			"status":                resp.Status,
			"client_id":             resp.ClientID,
			"access_token":          resp.AccessToken,
			"message":               resp.Message,
			"server_name":           serverName,
			"app_display_name":      appDisplayName,
			"announcement":          announcement,
			"announcement_interval": announcementInterval,
			"startup_media_enabled": startupMediaEnabled,
			"startup_media":         startupMedia,
			"startup_media_type":    startupMediaType,
			"startup_duration":      startupDuration,
			"startup_skip_after":    startupSkipAfter,
			"global_maintenance":    globalMaintenance,
			"backup_servers":        backupServers,
			"is_tester":             resp.IsTester,
		})
	} else {
		// pending 状态返回 202
		c.JSON(http.StatusAccepted, models.APIResponse{Code: 202, Message: resp.Message, Data: gin.H{
			"status":                resp.Status,
			"client_id":             resp.ClientID,
			"access_token":          resp.AccessToken,
			"message":               resp.Message,
			"server_name":           serverName,
			"app_display_name":      appDisplayName,
			"announcement":          announcement,
			"announcement_interval": announcementInterval,
			"startup_media_enabled": startupMediaEnabled,
			"startup_media":         startupMedia,
			"startup_media_type":    startupMediaType,
			"startup_duration":      startupDuration,
			"startup_skip_after":    startupSkipAfter,
			"global_maintenance":    globalMaintenance,
			"backup_servers":        backupServers,
			"is_tester":             resp.IsTester,
		}})
	}
}

// ── 客户端：验证令牌 (客户端调用) ─────────────────────

func (h *ClientHandler) Verify(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		// 统一 URL token 策略：与 AuthMiddleware 一致，受 enable_url_token 设置控制
		enableUrlToken, _ := h.channelSvc.GetSetting("enable_url_token")
		if enableUrlToken == "true" {
			token = c.Query("token")
		}
	}
	if token == "" {
		fail(c, 401, "缺少令牌")
		return
	}

	// Strip "Bearer " prefix
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	client, err := h.clientSvc.Validate(token, utils.GetRealClientIP(c))
	if err != nil {
		fail(c, 401, "令牌无效或已过期")
		return
	}

	// 记录/更新心跳
	sessionID := c.Query("session_id")
	if sessionID != "" {
		speedBytes, _ := strconv.ParseInt(c.Query("speed_bytes"), 10, 64)
		if err := h.streamProxy.UpdateHeartbeat(sessionID, speedBytes); err != nil {
			if err.Error() == "KICKED_TEMP" {
				c.JSON(403, models.APIResponse{Code: 403, Message: "KICKED_TEMP"})
				return
			}
		}
	}

	serverName, _ := h.channelSvc.GetSetting("server_name")
	appDisplayName, _ := h.channelSvc.GetSetting("app_display_name")

	announcement, _ := h.channelSvc.GetSetting("system_announcement")
	announcementIntervalStr, _ := h.channelSvc.GetSetting("system_announcement_interval")
	announcementInterval, _ := strconv.Atoi(announcementIntervalStr)

	// 开机短视频/广告
	startupMediaEnabledStr, _ := h.channelSvc.GetSetting("startup_media_enabled")
	startupMediaEnabled := startupMediaEnabledStr == "true"
	startupMedia, _ := h.channelSvc.GetSetting("startup_media_url")
	startupMediaType, _ := h.channelSvc.GetSetting("startup_media_type")
	if startupMediaType == "" {
		startupMediaType = "auto"
	}
	startupDurationStr, _ := h.channelSvc.GetSetting("startup_duration")
	startupDuration, _ := strconv.Atoi(startupDurationStr)
	if startupDuration == 0 {
		startupDuration = 5 // 默认5秒防死锁
	}
	startupSkipAfterStr, _ := h.channelSvc.GetSetting("startup_skip_after")
	startupSkipAfter, _ := strconv.Atoi(startupSkipAfterStr)

	maintenanceModeStr, _ := h.channelSvc.GetSetting("maintenance_mode")
	globalMaintenance := maintenanceModeStr == "true"

	backupServersStr, _ := h.channelSvc.GetSetting("server_backup_urls")
	var backupServers []string
	if backupServersStr != "" {
		for _, s := range strings.Split(backupServersStr, "\n") {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				encoded := base64.StdEncoding.EncodeToString([]byte(trimmed))
				backupServers = append(backupServers, encoded)
			}
		}
	}

	// 获取客户端远程配置（合并全局+单设备）
	var remoteConfig *models.ClientRemoteConfig
	if h.clientConfigSvc != nil {
		if cfg, err := h.clientConfigSvc.GetEffectiveConfig(client.ID); err == nil {
			remoteConfig = cfg
		} else {
			slog.Warn("client verify: failed to get effective config", "client_id", client.ID, "error", err)
		}
	}

	// 排队信息
	queuePosition := 0
	activeDeviceCount := 0
	planMaxStreams := 0
	if pid, exists := c.Get("plan_id"); exists {
		planID := pid.(int64)
		if ms, exists := c.Get("max_streams"); exists {
			planMaxStreams = ms.(int)
		}
		queuePosition = h.streamProxy.GetQueuePosition(planID, client.ID)
		if count, err := h.clientSvc.CountActiveDevices(planID, client.ID); err == nil {
			activeDeviceCount = count
		}
		// 如果活跃设备数未满且当前设备在排队，尝试出队
		if activeDeviceCount < planMaxStreams && queuePosition > 0 {
			if dequeued := h.streamProxy.TryDequeueNext(planID, activeDeviceCount, planMaxStreams); dequeued == client.ID {
				queuePosition = 0
			}
		}
	}

	ok(c, gin.H{
		"client_id":             client.ID,
		"name":                  client.Name,
		"max_streams":           client.MaxStreams,
		"plan_max_streams":      planMaxStreams,
		"active_device_count":   activeDeviceCount,
		"queue_position":        queuePosition,
		"expires_at":            client.ExpiresAt,
		"plan_name":             client.PlanName,
		"server_name":           serverName,
		"app_display_name":      appDisplayName,
		"announcement":          announcement,
		"announcement_interval": announcementInterval,
		"enable_log":            client.EnableLog,
		"startup_media_enabled": startupMediaEnabled,
		"startup_media":         startupMedia,
		"startup_media_type":    startupMediaType,
		"startup_duration":      startupDuration,
		"startup_skip_after":    startupSkipAfter,
		"global_maintenance":    globalMaintenance,
		"backup_servers":        backupServers,
		"is_tester":             client.IsTester,
		"client_config":         remoteConfig,
	})
}

// ── 客户端：查看自己状态 ───────────────────────────────

func (h *ClientHandler) Me(c *gin.Context) {
	token := extractToken(c)
	if token == "" {
		fail(c, 401, "缺少令牌")
		return
	}

	client, err := h.clientSvc.GetByToken(token)
	if err != nil {
		fail(c, 401, "无效令牌")
		return
	}

	full, err := h.clientSvc.GetByID(client.ID)
	if err != nil {
		fail(c, 404, "客户端不存在")
		return
	}

	ok(c, full)
}

// ── 管理端：客户端列表 ─────────────────────────────────

func (h *ClientHandler) List(c *gin.Context) {
	status := c.Query("status")
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	p := &models.PageRequest{Page: page, PageSize: pageSize}
	resp, err := h.clientSvc.List(status, search, p)
	if err != nil {
		slog.Error("client list failed", "error", err)
		fail(c, 500, "获取客户端列表失败")
		return
	}
	ok(c, resp)
}

// ── 管理端：获取单个客户端详情 ─────────────────────────

func (h *ClientHandler) Get(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	client, err := h.clientSvc.GetByID(id)
	if err != nil {
		fail(c, 404, "客户端不存在")
		return
	}
	ok(c, client)
}

// ── 管理端：审批 ───────────────────────────────────────

func (h *ClientHandler) Approve(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req models.ClientApproveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Warn("approve: invalid request body", "error", err)
	}

	if req.MaxDays > 999999 {
		req.MaxDays = 999999
	}
	if req.MaxStreams > 9999 {
		req.MaxStreams = 9999
	}

	approver := c.GetString("operator")
	if approver == "" {
		approver = "admin"
	}

	if err := h.clientSvc.Approve(id, &req, approver); err != nil {
		slog.Error("client approve failed", "client_id", id, "error", err)
		fail(c, 500, "审批操作失败")
		return
	}

	ok(c, gin.H{"message": "已审批通过"})
}

// ── 管理端：拒绝 ───────────────────────────────────────

func (h *ClientHandler) Reject(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req models.ClientRejectReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "请填写拒绝原因")
		return
	}

	if err := h.clientSvc.Reject(id, &req); err != nil {
		slog.Error("client reject failed", "client_id", id, "error", err)
		fail(c, 500, "拒绝操作失败")
		return
	}

	ok(c, gin.H{"message": "已拒绝"})
}

// ── 管理端：封禁 ───────────────────────────────────────

func (h *ClientHandler) Ban(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var body struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		slog.Warn("ban: invalid request body", "error", err)
	}

	if err := h.clientSvc.Ban(id, body.Reason); err != nil {
		slog.Error("client ban failed", "client_id", id, "error", err)
		fail(c, 500, "封禁操作失败")
		return
	}

	ok(c, gin.H{"message": "已封禁"})
}

// ── 管理端：解封 ───────────────────────────────────────

func (h *ClientHandler) Unban(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.clientSvc.Unban(id); err != nil {
		slog.Error("client unban failed", "client_id", id, "error", err)
		fail(c, 500, "解封操作失败")
		return
	}
	ok(c, gin.H{"message": "已解封"})
}

// ── 管理端：吊销令牌 ───────────────────────────────────

func (h *ClientHandler) RevokeToken(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.clientSvc.RevokeToken(id); err != nil {
		slog.Error("token revoke failed", "client_id", id, "error", err)
		fail(c, 500, "吊销操作失败")
		return
	}
	ok(c, gin.H{"message": "令牌已吊销"})
}

// ── 管理端：重新生成令牌 ───────────────────────────────

func (h *ClientHandler) RegenerateToken(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	token, err := h.clientSvc.RegenerateToken(id)
	if err != nil {
		slog.Error("token regenerate failed", "client_id", id, "error", err)
		fail(c, 500, "重新生成令牌失败")
		return
	}
	ok(c, gin.H{"token": token, "message": "新令牌已生成"})
}

// ── 管理端：批量操作 ───────────────────────────────────

func (h *ClientHandler) Batch(c *gin.Context) {
	var req models.ClientBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	approver := c.GetString("operator")
	if approver == "" {
		approver = "admin"
	}

	count, err := h.clientSvc.Batch(&req, approver)
	if err != nil {
		slog.Error("batch operation failed", "error", err, "action", req.Action, "ids", req.IDs)
		fail(c, 500, "批量操作失败")
		return
	}

	ok(c, gin.H{"affected": count})
}

// ── 管理端：删除客户端 ─────────────────────────────────

func (h *ClientHandler) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.clientSvc.Delete(id); err != nil {
		slog.Error("client delete failed", "client_id", id, "error", err)
		fail(c, 500, "删除失败")
		return
	}
	ok(c, gin.H{"message": "已删除"})
}

func (h *ClientHandler) UpdateRemark(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var body struct {
		Note string `json:"note"`
	}
	if err := c.BindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.clientSvc.UpdateRemark(id, body.Note); err != nil {
		slog.Error("update remark failed", "client_id", id, "error", err)
		fail(c, 500, "更新备注失败")
		return
	}
	ok(c, gin.H{"message": "已更新"})
}

// ── 管理端：访问日志 ───────────────────────────────────

func (h *ClientHandler) GetLogs(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	search := c.Query("search")

	logs, err := h.clientSvc.GetLogs(id, limit, search)
	if err != nil {
		slog.Error("get logs failed", "client_id", id, "error", err)
		fail(c, 500, "获取日志失败")
		return
	}
	ok(c, logs)
}

func (h *ClientHandler) GetRecentLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	search := c.Query("search")
	logs, err := h.clientSvc.GetRecentLogs(limit, search)
	if err != nil {
		slog.Error("get recent logs failed", "error", err)
		fail(c, 500, "获取日志失败")
		return
	}
	ok(c, logs)
}

// ── 管理端：统计 ───────────────────────────────────────

func (h *ClientHandler) GetStats(c *gin.Context) {
	total, pending, online := h.clientSvc.GetClientStats()
	ok(c, gin.H{
		"total_clients":   total,
		"pending_clients": pending,
		"online_clients":  online,
	})
}

// ── 辅助 ───────────────────────────────────────────────

func extractToken(c *gin.Context) string {
	token := c.GetHeader("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}
	if token == "" {
		token = c.Query("token")
	}
	return token
}

// ── 客户端日志上传 ───────────────────────────────────────

func (h *ClientHandler) UploadLog(c *gin.Context) {
	token := extractToken(c)
	if token == "" {
		fail(c, 401, "缺少令牌")
		return
	}
	client, err := h.clientSvc.Validate(token, utils.GetRealClientIP(c))
	if err != nil {
		fail(c, 401, "令牌无效或已过期")
		return
	}

	file, err := c.FormFile("log_file")
	if err != nil {
		fail(c, 400, "缺少日志文件")
		return
	}

	_ = os.MkdirAll("library/logs", 0755)
	logPath := fmt.Sprintf("library/logs/%s.log", client.DeviceID)

	// Check size (5MB limit)
	if stat, err := os.Stat(logPath); err == nil && stat.Size() > 5*1024*1024 {
		if err := os.Rename(logPath, logPath+".bak"); err != nil {
			slog.Warn("Failed to rotate log file", "error", err)
		}
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("打开日志文件失败", "error", err)
		fail(c, 500, "服务端错误")
		return
	}
	defer func() { _ = f.Close() }()

	src, err := file.Open()
	if err != nil {
		fail(c, 500, "读取上传文件失败")
		return
	}
	defer func() { _ = src.Close() }()

	if _, err := fmt.Fprintf(f, "\n--- Log Upload: %s ---\n", time.Now().Format(time.RFC3339)); err != nil {
		fail(c, 500, "写入日志失败")
		return
	}

	if _, err := io.Copy(f, src); err != nil {
		fail(c, 500, "写入日志失败")
		return
	}

	ok(c, gin.H{"message": "日志上传成功"})
}

func (h *ClientHandler) UpdateLogConfig(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var body struct {
		EnableLog bool `json:"enable_log"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.clientSvc.UpdateLogConfig(id, body.EnableLog); err != nil {
		failInternal(c, err, "更新配置失败")
		return
	}
	ok(c, gin.H{"message": "配置已更新"})
}

func (h *ClientHandler) SetTester(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var body struct {
		IsTester bool `json:"is_tester"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.clientSvc.SetTester(id, body.IsTester); err != nil {
		failInternal(c, err, "设置测试机失败")
		return
	}
	ok(c, gin.H{"message": "设置成功"})
}

func (h *ClientHandler) DownloadLog(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	client, err := h.clientSvc.GetByID(id)
	if err != nil {
		fail(c, 404, "设备不存在")
		return
	}

	logPath := fmt.Sprintf("library/logs/%s.log", client.DeviceID)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fail(c, 404, "暂无终端日志")
		return
	}

	c.FileAttachment(logPath, fmt.Sprintf("%s.log", client.DeviceID))
}

// ── 管理端：全局远程配置 ────────────────────────────────

// GetGlobalConfig 获取全局客户端配置列表
func (h *ClientHandler) GetGlobalConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		ok(c, []interface{}{})
		return
	}
	entries, err := h.clientConfigSvc.GetGlobalConfigs()
	if err != nil {
		failInternal(c, err, "获取全局配置失败")
		return
	}
	ok(c, entries)
}

// SaveGlobalConfig 批量保存全局客户端配置
func (h *ClientHandler) SaveGlobalConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		fail(c, 503, "配置服务未初始化")
		return
	}
	var req models.ClientConfigSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.clientConfigSvc.SaveGlobalConfigs(req.Configs, req.Hidden); err != nil {
		failInternal(c, err, "保存全局配置失败")
		return
	}
	ok(c, gin.H{"message": "全局配置已保存"})
}

// ── 管理端：单客户端远程配置 ───────────────────────────

// GetClientConfig 获取单客户端配置列表
func (h *ClientHandler) GetClientConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		ok(c, []interface{}{})
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	entries, err := h.clientConfigSvc.GetClientConfigs(id)
	if err != nil {
		failInternal(c, err, "获取设备配置失败")
		return
	}
	ok(c, entries)
}

// SaveClientConfig 批量保存单客户端配置
func (h *ClientHandler) SaveClientConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		fail(c, 503, "配置服务未初始化")
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req models.ClientConfigSaveReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.clientConfigSvc.SaveClientConfigs(id, req.Configs, req.Hidden); err != nil {
		failInternal(c, err, "保存设备配置失败")
		return
	}
	ok(c, gin.H{"message": "设备配置已保存"})
}

// DeleteClientConfig 删除单客户端某配置项（恢复继承全局）
func (h *ClientHandler) DeleteClientConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		fail(c, 503, "配置服务未初始化")
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	key := c.Param("key")
	if err := h.clientConfigSvc.DeleteClientConfig(id, key); err != nil {
		failInternal(c, err, "删除配置项失败")
		return
	}
	ok(c, gin.H{"message": "配置项已删除，将继承全局配置"})
}

// ResetClientConfig 清除单客户端所有配置（完全恢复全局）
func (h *ClientHandler) ResetClientConfig(c *gin.Context) {
	if h.clientConfigSvc == nil {
		fail(c, 503, "配置服务未初始化")
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.clientConfigSvc.DeleteAllClientConfigs(id); err != nil {
		failInternal(c, err, "重置设备配置失败")
		return
	}
	ok(c, gin.H{"message": "设备配置已重置，将全部继承全局配置"})
}
