package api

import (
	"golang.org/x/crypto/bcrypt"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"regexp"
	"path/filepath"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services"
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

func (h *Handler) getAdminPasswordHash() (string, error) {
	settings, err := h.channelSvc.GetAllSettings()
	if err != nil {
		return "", err
	}
	hash, exists := settings["admin_password_hash"]
	if !exists {
		defaultPwd := adminPassword
		if defaultPwd == "" {
			defaultPwd = "admin123"
		}
		hashedBytes, err := bcrypt.GenerateFromPassword([]byte(defaultPwd), bcrypt.DefaultCost)
		if err != nil {
			return "", err
		}
		hash = string(hashedBytes)
		_ = h.channelSvc.SetSetting("admin_password_hash", hash)
	}
	return hash, nil
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
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	p := &models.PageRequest{Page: page, PageSize: pageSize}
	var clientID int64
	if id, exists := c.Get("client_id"); exists {
		clientID = id.(int64)
	}
	resp, err := h.channelSvc.ListChannels(groupID, search, p, clientID)
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

			// 聚合后的列表
			var groupedItems []map[string]interface{}
			groupMap := make(map[string]int) // name -> index in groupedItems

			for i := range items {
				proxyURL := ""
				if !items[i].IsDirect {
					ext := "ts"
					switch items[i].StreamType {
					case "hls", "":
						ext = "m3u8"
					case "mp4", "flv", "mkv", "mpd":
						ext = items[i].StreamType
					}
					proxyURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d/play.%s?token=%s", baseURL, items[i].ID, ext, clientToken)
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

				if items[i].EPGChannelID != "" {
					title, pct := h.epgSvc.GetCurrentEPGWithProgress(items[i].EPGChannelID)
					items[i].CurrentEPG = title
					items[i].EpgPercent = pct
				} else {
					title, pct := h.epgSvc.GetCurrentEPGWithProgress(items[i].Name)
					items[i].CurrentEPG = title
					items[i].EpgPercent = pct
				}

				// 开始聚合
				nameKey := items[i].Name
				var linesForThisItem []map[string]interface{}

				if !items[i].IsDirect {
					// 代理模式下，下发一条指向服务端的代理地址即可（服务端自动实现多线路容灾换线）
					linesForThisItem = append(linesForThisItem, map[string]interface{}{
						"id":             items[i].ID,
						"stream_url":     proxyURL,
						"stream_type":    items[i].StreamType,
						"user_agent":     items[i].UserAgent,
						"custom_headers": items[i].CustomHeaders,
						"support_catchup": items[i].SupportCatchup,
						"catchup_days":    items[i].CatchupDays,
					})
				} else {
					// 直连模式下，把 "#" 拼接的多线路拆开下发给客户端，由客户端实现多线路容灾换线
					rawURLs := strings.Split(items[i].StreamURL, "#")
					for _, u := range rawURLs {
						if strings.TrimSpace(u) == "" { continue }
						linesForThisItem = append(linesForThisItem, map[string]interface{}{
							"id":             items[i].ID,
							"stream_url":     strings.TrimSpace(u),
							"stream_type":    items[i].StreamType,
							"user_agent":     items[i].UserAgent,
							"custom_headers": items[i].CustomHeaders,
							"support_catchup": items[i].SupportCatchup,
							"catchup_days":    items[i].CatchupDays,
						})
					}
				}

				if idx, exists := groupMap[nameKey]; exists {
					// 已存在该频道，将其作为新线路追加
					lines := groupedItems[idx]["lines"].([]map[string]interface{})
					groupedItems[idx]["lines"] = append(lines, linesForThisItem...)
				} else {
					// 新频道
					newGroup := map[string]interface{}{
						"id":          items[i].ID,
						"group_id":    items[i].GroupID,
						"name":        items[i].Name,
						"logo":        items[i].Logo,
						"description": items[i].Description,
						"current_epg": items[i].CurrentEPG,
						"epg_percent": items[i].EpgPercent,

						"sort_order":  items[i].SortOrder,
						"support_catchup": items[i].SupportCatchup,
						"catchup_days":    items[i].CatchupDays,
						"lines":       linesForThisItem,
					}
					groupedItems = append(groupedItems, newGroup)
					groupMap[nameKey] = len(groupedItems) - 1
				}
			}
			resp.Items = groupedItems
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

// ── Stream ─────────────────────────────────────────────

func (h *Handler) ProxyStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	subPath := c.Param("path")
	
	var clientID int64
	var clientName string
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	if cname, exists := c.Get("client_name"); exists {
		clientName = cname.(string)
	}
	clientIP := c.ClientIP()
	
	var targetURL string
	if subPath != "" && subPath != "/" && !strings.HasPrefix(subPath, "/play.") {
		ch, err := h.channelSvc.GetChannel(id, 0)
		if err == nil && ch.StreamURL != "" {
			baseURLStr := h.streamProxy.GetRedirectedURL(id)
			if baseURLStr == "" {
				baseURLStr = ch.StreamURL
			}
			base, err1 := url.Parse(baseURLStr)
			rel, err2 := url.Parse(strings.TrimPrefix(subPath, "/"))
			if err1 == nil && err2 == nil {
				resolved := base.ResolveReference(rel)
				// Remove our proxy token before forwarding query params to upstream
				q := c.Request.URL.Query()
				q.Del("token")
				
				// Keep the original upstream query params if this is just resolving a relative path
				// Note: if the upstream URL already has query params, they might get overwritten if not careful,
				// but ResolveReference replaces the query if rel has one.
				if len(q) > 0 {
				    // Combine with resolved query
				    resolvedQuery := resolved.Query()
				    for k, v := range q {
				        resolvedQuery[k] = v
				    }
				    resolved.RawQuery = resolvedQuery.Encode()
				}
				targetURL = resolved.String()
			}
		}
	}

	if err := h.streamProxy.ServeStream(id, clientID, clientIP, clientName, c.Writer, c.Request, targetURL); err != nil {
		slog.Error("stream proxy failed", "channel_id", id, "subPath", subPath, "error", err)
		// 流代理失败时 Writer 可能已经写入了 header，不能再写 JSON
		if !c.Writer.Written() {
			fail(c, 502, "流媒体代理失败")
		}
	}
}

func generateCatchupURL(streamURL, catchupSource string, startUnix, endUnix int64) string {
	if catchupSource == "" {
		return streamURL
	}
	
	start := time.Unix(startUnix, 0).In(time.Local)
	end := time.Unix(endUnix, 0).In(time.Local)
	
	source := catchupSource
	source = strings.ReplaceAll(source, "${(b)yyyyMMddHHmmss}", start.Format("20060102150405"))
	source = strings.ReplaceAll(source, "${(e)yyyyMMddHHmmss}", end.Format("20060102150405"))
	source = strings.ReplaceAll(source, "${b}", fmt.Sprintf("%d", startUnix))
	source = strings.ReplaceAll(source, "${e}", fmt.Sprintf("%d", endUnix))
	
	separator := ""
	if !strings.HasPrefix(source, "?") && !strings.HasPrefix(source, "&") {
		if strings.Contains(streamURL, "?") {
			separator = "&"
		} else {
			separator = "?"
		}
	} else if strings.HasPrefix(source, "?") && strings.Contains(streamURL, "?") {
		source = "&" + source[1:]
	}
	
	return streamURL + separator + source
}

func (h *Handler) CatchupStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	startStr := c.Query("start")
	endStr := c.Query("end")
	
	startUnix, _ := strconv.ParseInt(startStr, 10, 64)
	endUnix, _ := strconv.ParseInt(endStr, 10, 64)
	
	ch, err := h.channelSvc.GetChannel(id, 0)
	if err != nil {
		fail(c, 404, "频道不存在")
		return
	}
	
	if !ch.SupportCatchup {
		fail(c, 400, "该频道不支持回看")
		return
	}
	
	targetURL := generateCatchupURL(ch.StreamURL, ch.CatchupSource, startUnix, endUnix)
	
	if ch.IsDirect {
		c.Redirect(http.StatusFound, targetURL)
		return
	}
	
	var clientID int64
	var clientName string
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	if cname, exists := c.Get("client_name"); exists {
		clientName = cname.(string)
	}
	clientIP := c.ClientIP()

	if err := h.streamProxy.ServeStream(id, clientID, clientIP, clientName, c.Writer, c.Request, targetURL); err != nil {
		slog.Error("catchup stream proxy failed", "channel_id", id, "error", err)
		if !c.Writer.Written() {
			fail(c, 502, "回看流代理失败")
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
	status, _ := h.streamProxy.CheckHealth(ch.ID, ch.StreamURL, ch.StreamType)
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

// ── App Update ─────────────────────────────────────────

func (h *Handler) GetAppUpdate(c *gin.Context) {
	settings, err := h.channelSvc.GetAllSettings()
	if err != nil {
		failInternal(c, err, "获取配置失败")
		return
	}
	
	manualUpdate := models.AppUpdateConfig{}
	if val, ok := settings["update_version_code"]; ok {
		manualUpdate.VersionCode, _ = strconv.Atoi(val)
	}
	if val, ok := settings["update_version_name"]; ok {
		manualUpdate.VersionName = val
	}
	if val, ok := settings["update_download_url"]; ok {
		manualUpdate.DownloadURL = val
	}
	if val, ok := settings["update_log"]; ok {
		manualUpdate.UpdateLog = val
	}
	if val, ok := settings["update_force"]; ok {
		manualUpdate.ForceUpdate = val == "true"
	}
	
	// Scan local filesystem
	requestedArch := c.Query("arch")
	if requestedArch == "" {
		requestedArch = "all"
	}

	downloadDir := "./web/download"
	entries, err := os.ReadDir(downloadDir)
	
	var maxLocalVersionCode int
	var maxLocalVersionName string
	var maxLocalFolderName string
	
	// Format: {versionCode}_{versionName} e.g. 10_v1.0.3
	re := regexp.MustCompile(`^(\d+)_(.+)$`)
	
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				matches := re.FindStringSubmatch(entry.Name())
				if len(matches) == 3 {
					vCode, _ := strconv.Atoi(matches[1])
					vName := matches[2]
					if vCode > maxLocalVersionCode {
						maxLocalVersionCode = vCode
						maxLocalVersionName = vName
						maxLocalFolderName = entry.Name()
					}
				}
			}
		}
	}

	// Compare and select
	if maxLocalVersionCode > 0 && maxLocalVersionCode >= manualUpdate.VersionCode {
		// Use local filesystem
		localUpdate := models.AppUpdateConfig{
			VersionCode: maxLocalVersionCode,
			VersionName: maxLocalVersionName,
			ForceUpdate: false, // Default for local files
		}
		
		// Find best APK match
		apkDir := filepath.Join(downloadDir, maxLocalFolderName)
		apkEntries, _ := os.ReadDir(apkDir)
		
		var bestApk string
		var fallbackApk string
		var anyApk string
		
		for _, f := range apkEntries {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".apk") {
				anyApk = f.Name()
				if strings.Contains(f.Name(), requestedArch) {
					bestApk = f.Name()
				} else if strings.Contains(f.Name(), "all") || strings.Contains(f.Name(), "universal") {
					fallbackApk = f.Name()
				}
			}
		}
		
		selectedApk := bestApk
		if selectedApk == "" {
			selectedApk = fallbackApk
		}
		if selectedApk == "" {
			selectedApk = anyApk
		}
		
		if selectedApk != "" {
			scheme := "http"
			if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			baseURL := scheme + "://" + c.Request.Host
			localUpdate.DownloadURL = baseURL + "/download/" + maxLocalFolderName + "/" + selectedApk
		}
		
		// Read version.txt for update log
		logPath := filepath.Join(apkDir, "version.txt")
		if logBytes, err := os.ReadFile(logPath); err == nil {
			localUpdate.UpdateLog = string(logBytes)
		}
		
		ok(c, localUpdate)
		return
	}
	
	ok(c, manualUpdate)
}

func (h *Handler) SetAppUpdate(c *gin.Context) {
	var body models.AppUpdateConfig
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	
	_ = h.channelSvc.SetSetting("update_version_code", strconv.Itoa(body.VersionCode))
	_ = h.channelSvc.SetSetting("update_version_name", body.VersionName)
	_ = h.channelSvc.SetSetting("update_download_url", body.DownloadURL)
	_ = h.channelSvc.SetSetting("update_log", body.UpdateLog)
	
	forceVal := "false"
	if body.ForceUpdate {
		forceVal = "true"
	}
	_ = h.channelSvc.SetSetting("update_force", forceVal)
	
	ok(c, body)
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

	hash, err := h.getAdminPasswordHash()
	if err != nil {
		failInternal(c, err, "获取管理员凭证失败")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)); err != nil {
		slog.Warn("admin login failed", "ip", c.ClientIP(), "err", err)
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

func (h *Handler) UpdateAdminPassword(c *gin.Context) {
	var body struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	hash, err := h.getAdminPasswordHash()
	if err != nil {
		failInternal(c, err, "获取旧密码哈希失败")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.OldPassword)); err != nil {
		fail(c, 401, "原密码错误")
		return
	}

	newHashedBytes, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		failInternal(c, err, "密码加密失败")
		return
	}

	if err := h.channelSvc.SetSetting("admin_password_hash", string(newHashedBytes)); err != nil {
		failInternal(c, err, "更新密码失败")
		return
	}

	ok(c, gin.H{"message": "密码修改成功"})
}

// ── Server Stats ───────────────────────────────────────

func (h *Handler) GetStats(c *gin.Context) {
	p := &models.PageRequest{Page: 1, PageSize: 1}
	totalResp, _ := h.channelSvc.ListChannels(0, "", p, 0)
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
