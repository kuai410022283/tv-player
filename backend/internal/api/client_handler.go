package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services"
)

type ClientHandler struct {
	clientSvc  *services.ClientService
	channelSvc *services.ChannelService
}

func NewClientHandler(clientSvc *services.ClientService, channelSvc *services.ChannelService) *ClientHandler {
	return &ClientHandler{clientSvc: clientSvc, channelSvc: channelSvc}
}

// ── 客户端：注册 ───────────────────────────────────────

func (h *ClientHandler) Register(c *gin.Context) {
	var req models.ClientRegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误: 设备名称和设备ID必填")
		return
	}

	ip := c.ClientIP()
	resp, err := h.clientSvc.Register(&req, ip)
	if err != nil {
		slog.Error("client register failed", "error", err, "device_id", req.DeviceID)
		fail(c, 500, "注册失败，请稍后重试")
		return
	}

	h.clientSvc.AddLog(resp.ClientID, "register", 0, ip, c.GetHeader("User-Agent"), "")

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

	if resp.Status == "approved" {
		ok(c, gin.H{
			"status":                resp.Status,
			"client_id":             resp.ClientID,
			"access_token":          resp.AccessToken,
			"message":               resp.Message,
			"announcement":          announcement,
			"announcement_interval": announcementInterval,
			"startup_media_enabled": startupMediaEnabled,
			"startup_media":         startupMedia,
			"startup_media_type":    startupMediaType,
			"startup_duration":      startupDuration,
			"startup_skip_after":    startupSkipAfter,
			"global_maintenance":    globalMaintenance,
			"is_tester":             resp.IsTester,
		})
	} else {
		// pending 状态返回 202
		c.JSON(http.StatusAccepted, models.APIResponse{Code: 202, Message: resp.Message, Data: gin.H{
			"status":                resp.Status,
			"client_id":             resp.ClientID,
			"access_token":          resp.AccessToken,
			"message":               resp.Message,
			"announcement":          announcement,
			"announcement_interval": announcementInterval,
			"startup_media_enabled": startupMediaEnabled,
			"startup_media":         startupMedia,
			"startup_media_type":    startupMediaType,
			"startup_duration":      startupDuration,
			"startup_skip_after":    startupSkipAfter,
			"global_maintenance":    globalMaintenance,
			"is_tester":             resp.IsTester,
		}})
	}
}

// ── 客户端：验证令牌 (客户端调用) ─────────────────────

func (h *ClientHandler) Verify(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		token = c.Query("token")
	}
	if token == "" {
		fail(c, 401, "缺少令牌")
		return
	}

	// Strip "Bearer " prefix
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	client, err := h.clientSvc.Validate(token, c.ClientIP())
	if err != nil {
		fail(c, 401, "令牌无效或已过期")
		return
	}

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

	ok(c, gin.H{
		"client_id":             client.ID,
		"name":                  client.Name,
		"max_streams":           client.MaxStreams,
		"expires_at":            client.ExpiresAt,
		"plan_name":             client.PlanName,
		"announcement":          announcement,
		"announcement_interval": announcementInterval,
		"enable_log":            client.EnableLog,
		"startup_media_enabled": startupMediaEnabled,
		"startup_media":         startupMedia,
		"startup_media_type":    startupMediaType,
		"startup_duration":      startupDuration,
		"startup_skip_after":    startupSkipAfter,
		"global_maintenance":    globalMaintenance,
		"is_tester":             client.IsTester,
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
	c.ShouldBindJSON(&req)

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
	c.ShouldBindJSON(&body)

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
	client, err := h.clientSvc.Validate(token, c.ClientIP())
	if err != nil {
		fail(c, 401, "令牌无效或已过期")
		return
	}

	file, err := c.FormFile("log_file")
	if err != nil {
		fail(c, 400, "缺少日志文件")
		return
	}

	os.MkdirAll("library/logs", 0755)
	logPath := fmt.Sprintf("library/logs/%s.log", client.DeviceID)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Error("打开日志文件失败", "error", err)
		fail(c, 500, "服务端错误")
		return
	}
	defer f.Close()

	src, err := file.Open()
	if err != nil {
		fail(c, 500, "读取上传文件失败")
		return
	}
	defer src.Close()

	if _, err := f.WriteString(fmt.Sprintf("\n--- Log Upload: %s ---\n", time.Now().Format(time.RFC3339))); err != nil {
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
