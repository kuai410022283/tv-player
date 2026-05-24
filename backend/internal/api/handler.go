package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/tvplayer/backend/internal/models"
	"github.com/tvplayer/backend/internal/services"
)

// Version 由编译时注入: go build -ldflags "-X main.Version=v1.0.0"
var Version = "dev"

// startTime 记录服务启动时间，用于 uptime 统计
var startTime = time.Now()

// jwtSecret 存储 JWT 密钥，由 Init 设置
var jwtSecret string
var adminPassword string
var jwtExpireHours int = 720

// InitSecret 设置 JWT 密钥和管理员密码
func InitSecret(secret, password string, expireHours int) {
	jwtSecret = secret
	adminPassword = password
	if expireHours > 0 {
		jwtExpireHours = expireHours
	}
}

func getJWTSecret() string {
	if jwtSecret != "" {
		return jwtSecret
	}
	return ""
}

func getAdminPassword() string {
	if adminPassword != "" {
		return adminPassword
	}
	if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		return p
	}
	return ""
}

func generateAdminToken(secret string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "admin",
		"iss":  "MediaPlayer",
		"iat":  now.Unix(),
		"exp":  now.Add(time.Duration(jwtExpireHours) * time.Hour).Unix(),
	})
	return token.SignedString([]byte(secret))
}

type Handler struct {
	channelSvc  *services.ChannelService
	streamProxy *services.StreamProxy
	importer    *services.M3UImporter
	clientSvc   *services.ClientService
	epgSvc      *services.EPGService
}

func NewHandler(channelSvc *services.ChannelService, streamProxy *services.StreamProxy, importer *services.M3UImporter, clientSvc *services.ClientService, epgSvc *services.EPGService) *Handler {
	return &Handler{
		channelSvc:  channelSvc,
		streamProxy: streamProxy,
		importer:    importer,
		clientSvc:   clientSvc,
		epgSvc:      epgSvc,
	}
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "ok", Data: data})
}

// fail 返回用户友好的错误信息，不泄露内部细节
func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, models.APIResponse{Code: code, Message: msg})
}

// failInternal 记录内部错误并返回通用错误信息
func failInternal(c *gin.Context, err error, userMsg string) {
	slog.Error("internal error", "path", c.Request.URL.Path, "error", err)
	c.JSON(http.StatusInternalServerError, models.APIResponse{Code: 500, Message: userMsg})
}

// ── Groups ─────────────────────────────────────────────

func (h *Handler) ListGroups(c *gin.Context) {
	var clientID int64
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	groups, err := h.channelSvc.ListGroups(clientID)
	if err != nil {
		failInternal(c, err, "获取分组列表失败")
		return
	}
	ok(c, groups)
}

func (h *Handler) AdminListGroups(c *gin.Context) {
	var p models.PageRequest
	if err := c.ShouldBindQuery(&p); err != nil {
		p = models.PageRequest{Page: 1, PageSize: 20}
	}
	search := c.Query("search")

	res, err := h.channelSvc.AdminListGroups(search, &p)
	if err != nil {
		failInternal(c, err, "获取分组列表失败")
		return
	}
	ok(c, res)
}

func (h *Handler) CreateGroup(c *gin.Context) {
	var g models.ChannelGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.CreateGroup(&g); err != nil {
		failInternal(c, err, "创建分组失败")
		return
	}
	ok(c, g)
}

func (h *Handler) UpdateGroup(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var g models.ChannelGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	g.ID = id
	if err := h.channelSvc.UpdateGroup(&g); err != nil {
		failInternal(c, err, "更新分组失败")
		return
	}
	ok(c, g)
}

func (h *Handler) DeleteGroup(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteGroup(id); err != nil {
		failInternal(c, err, "删除分组失败")
		return
	}
	ok(c, nil)
}

func (h *Handler) BatchGroup(c *gin.Context) {
	var req struct {
		IDs    []int64 `json:"ids" binding:"required"`
		Action string  `json:"action" binding:"required"` // only "delete" supported for now
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	if req.Action == "delete" {
		if err := h.channelSvc.BatchDeleteGroups(req.IDs); err != nil {
			failInternal(c, err, "批量删除失败")
			return
		}
	}
	ok(c, nil)
}

// ── Channels ───────────────────────────────────────────

func (h *Handler) ListChannels(c *gin.Context) {
	groupID, _ := strconv.ParseInt(c.Query("group_id"), 10, 64)
	favorite := c.Query("favorite") == "true"
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	p := &models.PageRequest{Page: page, PageSize: pageSize}
	var clientID int64
	if id, exists := c.Get("client_id"); exists {
		clientID = id.(int64)
	}
	resp, err := h.channelSvc.ListChannels(groupID, favorite, search, p, clientID)
	if err != nil {
		failInternal(c, err, "获取频道列表失败")
		return
	}

	// 如果请求来自客户端，动态注入代理地址、解析继承防盗链头参数
	authType, _ := c.Get("auth_type")
	if authType == "client" {
		if items, ok := resp.Items.([]models.Channel); ok {
			host := c.Request.Host
			scheme := "http"
			if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			baseURL := scheme + "://" + host
			
			clientToken := ""
			if t, exists := c.Get("client_token"); exists {
				clientToken = t.(string)
			}

			for i := range items {
				if !items[i].IsDirect {
					items[i].StreamURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d?token=%s", baseURL, items[i].ID, clientToken)
				}
				// 无论直连还是代理模式，客户端都拿取继承所得的 UA 与 CustomHeaders 方便统一标准播放
				if ua, headers, err := h.channelSvc.GetInheritedHeaders(items[i].ID); err == nil {
					items[i].UserAgent = ua
					if len(headers) > 0 {
						if b, err := json.Marshal(headers); err == nil {
							items[i].CustomHeaders = string(b)
						}
					} else {
						items[i].CustomHeaders = ""
					}
				}
			}
			resp.Items = items
		}
	}

	ok(c, resp)
}

func (h *Handler) GetChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var clientID int64
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	ch, err := h.channelSvc.GetChannel(id, clientID)
	if err != nil {
		fail(c, 404, "频道不存在")
		return
	}

	authType, _ := c.Get("auth_type")
	if authType == "client" {
		host := c.Request.Host
		scheme := "http"
		if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		baseURL := scheme + "://" + host
		
		clientToken := ""
		if t, exists := c.Get("client_token"); exists {
			clientToken = t.(string)
		}

		if !ch.IsDirect {
			ch.StreamURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d?token=%s", baseURL, ch.ID, clientToken)
		}
		if ua, headers, err := h.channelSvc.GetInheritedHeaders(ch.ID); err == nil {
			ch.UserAgent = ua
			if len(headers) > 0 {
				if b, err := json.Marshal(headers); err == nil {
					ch.CustomHeaders = string(b)
				}
			} else {
				ch.CustomHeaders = ""
			}
		}
	}

	ok(c, ch)
}

func (h *Handler) CreateChannel(c *gin.Context) {
	var ch models.Channel
	if err := c.ShouldBindJSON(&ch); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.CreateChannel(&ch); err != nil {
		failInternal(c, err, "创建频道失败")
		return
	}
	ok(c, ch)
}

func (h *Handler) UpdateChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var ch models.Channel
	if err := c.ShouldBindJSON(&ch); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	ch.ID = id
	if err := h.channelSvc.UpdateChannel(&ch); err != nil {
		failInternal(c, err, "更新频道失败")
		return
	}
	ok(c, ch)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteChannel(id); err != nil {
		failInternal(c, err, "删除频道失败")
		return
	}
	ok(c, nil)
}

func (h *Handler) BatchChannel(c *gin.Context) {
	var req struct {
		IDs    []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.BatchDeleteChannels(req.IDs); err != nil {
		failInternal(c, err, "批量删除失败")
		return
	}
	ok(c, nil)
}

func (h *Handler) ToggleFavorite(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var clientID int64
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	if clientID == 0 {
		fail(c, 403, "缺少客户端授权")
		return
	}
	if err := h.channelSvc.ToggleFavorite(id, clientID); err != nil {
		failInternal(c, err, "操作失败")
		return
	}
	ok(c, nil)
}

// ── Stream ─────────────────────────────────────────────

func (h *Handler) ProxyStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	
	var clientID int64
	var clientName string
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	if cname, exists := c.Get("client_name"); exists {
		clientName = cname.(string)
	}
	clientIP := c.ClientIP()

	if err := h.streamProxy.ServeStream(id, clientID, clientIP, clientName, c.Writer, c.Request); err != nil {
		slog.Error("stream proxy failed", "channel_id", id, "error", err)
		// 流代理失败时 Writer 可能已经写入了 header，不能再写 JSON
		if !c.Writer.Written() {
			fail(c, 502, "流媒体代理失败")
		}
	}
}

func (h *Handler) CheckStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ch, err := h.channelSvc.GetChannel(id, 0)
	if err != nil {
		fail(c, 404, "频道不存在")
		return
	}
	status, _ := h.streamProxy.CheckHealth(ch.StreamURL, ch.StreamType)
	ok(c, status)
}

func (h *Handler) GetActiveStreams(c *gin.Context) {
	streams := h.streamProxy.GetActiveStreams()
	ok(c, streams)
}

func (h *Handler) KillStream(c *gin.Context) {
	sessionID := c.Param("id")
	if h.streamProxy.KillStream(sessionID) {
		ok(c, "ok")
	} else {
		fail(c, 404, "Stream not found or already disconnected")
	}
}

// ── M3U Sources ────────────────────────────────────────

func (h *Handler) ListM3USources(c *gin.Context) {
	sources, err := h.channelSvc.ListM3USources()
	if err != nil {
		failInternal(c, err, "获取 M3U 源列表失败")
		return
	}
	ok(c, sources)
}

func (h *Handler) AddM3USource(c *gin.Context) {
	var src models.M3USource
	if err := c.ShouldBindJSON(&src); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.AddM3USource(&src); err != nil {
		failInternal(c, err, "添加 M3U 源失败")
		return
	}
	ok(c, src)
}

func (h *Handler) UpdateM3USource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var src models.M3USource
	if err := c.ShouldBindJSON(&src); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	src.ID = id
	if err := h.channelSvc.UpdateM3USource(&src); err != nil {
		failInternal(c, err, "更新 M3U 源失败")
		return
	}
	ok(c, src)
}

func (h *Handler) ImportM3U(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	
	// 异步后台执行，避免阻塞前端并触发超时
	go func() {
		slog.Info("开始后台同步 M3U 源", "source_id", id)
		count, err := h.importer.ImportFromURL(id)
		if err != nil {
			slog.Error("后台同步 M3U 失败", "source_id", id, "error", err)
		} else {
			slog.Info("后台同步 M3U 完成", "source_id", id, "imported_channels", count)
		}
	}()
	
	ok(c, gin.H{"message": "已投递到后台同步中"})
}

func (h *Handler) ImportM3UString(c *gin.Context) {
	var body struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Content == "" {
		fail(c, 400, "参数错误")
		return
	}
	if body.Name == "" {
		body.Name = "粘贴导入"
	}
	count, err := h.importer.ImportFromString(body.Content, body.Name)
	if err != nil {
		slog.Error("M3U string import failed", "error", err)
		fail(c, 500, "导入失败，请检查 M3U 格式")
		return
	}
	ok(c, gin.H{"imported": count})
}

func (h *Handler) DeleteM3USource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteM3USource(id); err != nil {
		failInternal(c, err, "删除 M3U 源失败")
		return
	}
	ok(c, nil)
}

// ── History ────────────────────────────────────────────

func (h *Handler) GetHistory(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.channelSvc.GetHistory(limit)
	if err != nil {
		failInternal(c, err, "获取播放历史失败")
		return
	}
	ok(c, items)
}

func (h *Handler) AddHistory(c *gin.Context) {
	var hist models.PlayHistory
	if err := c.ShouldBindJSON(&hist); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.AddHistory(&hist); err != nil {
		failInternal(c, err, "记录播放历史失败")
		return
	}
	ok(c, hist)
}

// ── Settings ───────────────────────────────────────────

func (h *Handler) GetSettings(c *gin.Context) {
	settings, err := h.channelSvc.GetAllSettings()
	if err != nil {
		failInternal(c, err, "获取设置失败")
		return
	}
	ok(c, settings)
}

func (h *Handler) SetSetting(c *gin.Context) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.SetSetting(body.Key, body.Value); err != nil {
		failInternal(c, err, "保存设置失败")
		return
	}
	ok(c, nil)
}

// ── EPG ────────────────────────────────────────────────

func (h *Handler) GetEPG(c *gin.Context) {
	channelID := c.Query("channel_id")
	if channelID == "" {
		fail(c, 400, "请提供 channel_id")
		return
	}
	
	dateStr := c.Query("date")
	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	} else if len(dateStr) == 8 && !strings.Contains(dateStr, "-") {
		// 兼容 YYYYMMDD 格式，将其转换为内部使用的 YYYY-MM-DD 格式
		dateStr = dateStr[:4] + "-" + dateStr[4:6] + "-" + dateStr[6:]
	}
	
	programs := h.epgSvc.GetEPG(channelID, dateStr)
	ok(c, programs)
}

func (h *Handler) RefreshEPG(c *gin.Context) {
	if err := h.epgSvc.ForceRefresh(); err != nil {
		failInternal(c, err, "刷新 EPG 失败")
		return
	}
	ok(c, gin.H{"message": "EPG 数据已重新拉取并构建索引"})
}

// ── Admin Login ────────────────────────────────────────

func (h *Handler) AdminLogin(c *gin.Context) {
	var body struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "请提供密码")
		return
	}

	pwd := getAdminPassword()
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(pwd)) != 1 {
		slog.Warn("admin login failed", "ip", c.ClientIP())
		fail(c, 401, "密码错误")
		return
	}

	token, err := generateAdminToken(getJWTSecret())
	if err != nil {
		failInternal(c, err, "生成令牌失败")
		return
	}

	ok(c, gin.H{"token": token, "message": "登录成功"})
}

// ── Server Stats ───────────────────────────────────────

func (h *Handler) GetStats(c *gin.Context) {
	p := &models.PageRequest{Page: 1, PageSize: 1}
	totalResp, _ := h.channelSvc.ListChannels(0, false, "", p, 0)
	totalChannels := int64(0)
	if totalResp != nil {
		totalChannels = totalResp.Total
	}

	var onlineChannels int64
	_ = h.channelSvc.CountByStatus("online", &onlineChannels)

	totalClients, pendingClients, onlineClients := 0, 0, 0
	if h.clientSvc != nil {
		totalClients, pendingClients, onlineClients = h.clientSvc.GetClientStats()
	}

	var memMB int64
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB = int64(m.Alloc / 1024 / 1024)

	uptime := int64(time.Since(startTime).Seconds())

	stats := models.ServerStats{
		TotalChannels:  int(totalChannels),
		OnlineChannels: int(onlineChannels),
		ActiveStreams:  len(h.streamProxy.GetActiveStreams()),
		TotalClients:   totalClients,
		PendingClients: pendingClients,
		OnlineClients:  onlineClients,
		Uptime:         uptime,
		MemoryMB:       memMB,
	}
	ok(c, stats)
}

// ── Version ────────────────────────────────────────────

func (h *Handler) GetVersion(c *gin.Context) {
	ok(c, gin.H{
		"version":    Version,
		"go_version": runtime.Version(),
		"started_at": startTime.Format(time.RFC3339),
	})
}
