package api

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/tvplayer/backend/internal/models"
	"github.com/tvplayer/backend/internal/services"
)

// startTime 记录服务启动时间，用于 uptime 统计
var startTime = time.Now()

// jwtSecret 存储 JWT 密钥，由 Init 设置
var jwtSecret string
var adminPassword string

// InitSecret 设置 JWT 密钥和管理员密码
func InitSecret(secret, password string) {
	jwtSecret = secret
	adminPassword = password
}

func getJWTSecret() string {
	if jwtSecret != "" {
		return jwtSecret
	}
	return "tvplayer-secret-key-change-me"
}

func getAdminPassword() string {
	if adminPassword != "" {
		return adminPassword
	}
	if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		return p
	}
	return "admin123"
}

func generateAdminToken(secret string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "admin",
		"exp":  time.Now().Add(24 * time.Hour).Unix(),
	})
	return token.SignedString([]byte(secret))
}

type Handler struct {
	channelSvc  *services.ChannelService
	streamProxy *services.StreamProxy
	importer    *services.M3UImporter
	clientSvc   *services.ClientService
}

func NewHandler(channelSvc *services.ChannelService, streamProxy *services.StreamProxy, importer *services.M3UImporter, clientSvc *services.ClientService) *Handler {
	return &Handler{
		channelSvc:  channelSvc,
		streamProxy: streamProxy,
		importer:    importer,
		clientSvc:   clientSvc,
	}
}

func ok(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "ok", Data: data})
}

func fail(c *gin.Context, code int, msg string) {
	c.JSON(code, models.APIResponse{Code: code, Message: msg})
}

// ── Groups ─────────────────────────────────────────────

func (h *Handler) ListGroups(c *gin.Context) {
	groups, err := h.channelSvc.ListGroups()
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, groups)
}

func (h *Handler) CreateGroup(c *gin.Context) {
	var g models.ChannelGroup
	if err := c.ShouldBindJSON(&g); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.CreateGroup(&g); err != nil {
		fail(c, 500, err.Error())
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
		fail(c, 500, err.Error())
		return
	}
	ok(c, g)
}

func (h *Handler) DeleteGroup(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteGroup(id); err != nil {
		fail(c, 500, err.Error())
		return
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
	resp, err := h.channelSvc.ListChannels(groupID, favorite, search, p)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, resp)
}

func (h *Handler) GetChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ch, err := h.channelSvc.GetChannel(id)
	if err != nil {
		fail(c, 404, "频道不存在")
		return
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
		fail(c, 500, err.Error())
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
		fail(c, 500, err.Error())
		return
	}
	ok(c, ch)
}

func (h *Handler) DeleteChannel(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteChannel(id); err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, nil)
}

func (h *Handler) ToggleFavorite(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.ToggleFavorite(id); err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, nil)
}

// ── Stream ─────────────────────────────────────────────

func (h *Handler) ProxyStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.streamProxy.ServeStream(id, c.Writer, c.Request); err != nil {
		fail(c, 502, "流媒体代理失败: "+err.Error())
	}
}

func (h *Handler) CheckStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	ch, err := h.channelSvc.GetChannel(id)
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

// ── M3U Sources ────────────────────────────────────────

func (h *Handler) ListM3USources(c *gin.Context) {
	sources, err := h.channelSvc.ListM3USources()
	if err != nil {
		fail(c, 500, err.Error())
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
		fail(c, 500, err.Error())
		return
	}
	ok(c, src)
}

func (h *Handler) ImportM3U(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	count, err := h.importer.ImportFromURL(id)
	if err != nil {
		fail(c, 500, "导入失败: "+err.Error())
		return
	}
	ok(c, gin.H{"imported": count})
}

func (h *Handler) ImportM3UString(c *gin.Context) {
	var body struct {
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Content == "" {
		fail(c, 400, "参数错误")
		return
	}
	count, err := h.importer.ImportFromString(body.Content)
	if err != nil {
		fail(c, 500, "导入失败: "+err.Error())
		return
	}
	ok(c, gin.H{"imported": count})
}

func (h *Handler) DeleteM3USource(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.channelSvc.DeleteM3USource(id); err != nil {
		fail(c, 500, err.Error())
		return
	}
	ok(c, nil)
}

// ── History ────────────────────────────────────────────

func (h *Handler) GetHistory(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	items, err := h.channelSvc.GetHistory(limit)
	if err != nil {
		fail(c, 500, err.Error())
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
		fail(c, 500, err.Error())
		return
	}
	ok(c, hist)
}

// ── Settings ───────────────────────────────────────────

func (h *Handler) GetSettings(c *gin.Context) {
	settings, err := h.channelSvc.GetAllSettings()
	if err != nil {
		fail(c, 500, err.Error())
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
		fail(c, 500, err.Error())
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
	programs, err := h.channelSvc.GetEPGPrograms(channelID)
	if err != nil {
		ok(c, []interface{}{})
		return
	}
	ok(c, programs)
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

	// 从配置中获取 admin 密码 (通过环境变量或默认值)
	adminPassword := getAdminPassword()
	if body.Password != adminPassword {
		fail(c, 401, "密码错误")
		return
	}

	// 生成 JWT token
	token, err := generateAdminToken(getJWTSecret())
	if err != nil {
		fail(c, 500, "生成令牌失败")
		return
	}

	ok(c, gin.H{"token": token, "message": "登录成功"})
}

// ── Server Stats ───────────────────────────────────────

func (h *Handler) GetStats(c *gin.Context) {
	p := &models.PageRequest{Page: 1, PageSize: 1}
	totalResp, _ := h.channelSvc.ListChannels(0, false, "", p)
	totalChannels := int64(0)
	if totalResp != nil {
		totalChannels = totalResp.Total
	}

	// 统计在线频道数 (需要按 status 过滤)
	var onlineChannels int64
	h.channelSvc.CountByStatus("online", &onlineChannels)

	// 客户端统计
	totalClients, pendingClients, onlineClients := 0, 0, 0
	if h.clientSvc != nil {
		totalClients, pendingClients, onlineClients = h.clientSvc.GetClientStats()
	}

	// 内存统计
	var memMB int64
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	memMB = int64(m.Alloc / 1024 / 1024)

	// 运行时长
	uptime := int64(time.Since(startTime).Seconds())

	stats := models.ServerStats{
		TotalChannels:  int(totalChannels),
		OnlineChannels: int(onlineChannels),
		ActiveStreams:   len(h.streamProxy.GetActiveStreams()),
		TotalClients:   totalClients,
		PendingClients: pendingClients,
		OnlineClients:  onlineClients,
		Uptime:         uptime,
		MemoryMB:       memMB,
	}
	ok(c, stats)
}
