package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tvplayer/backend/internal/models"
	"github.com/tvplayer/backend/internal/services"
)

type ClientHandler struct {
	clientSvc *services.ClientService
}

func NewClientHandler(clientSvc *services.ClientService) *ClientHandler {
	return &ClientHandler{clientSvc: clientSvc}
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

	if resp.Status == "approved" {
		ok(c, resp)
	} else {
		// pending 状态返回 202
		c.JSON(http.StatusAccepted, models.APIResponse{Code: 202, Message: resp.Message, Data: resp})
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

	ok(c, gin.H{
		"client_id":    client.ID,
		"name":         client.Name,
		"max_streams":  client.MaxStreams,
		"expires_at":   client.ExpiresAt,
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

	logs, err := h.clientSvc.GetLogs(id, limit)
	if err != nil {
		slog.Error("get logs failed", "client_id", id, "error", err)
		fail(c, 500, "获取日志失败")
		return
	}
	ok(c, logs)
}

func (h *ClientHandler) GetRecentLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	logs, err := h.clientSvc.GetRecentLogs(limit)
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
