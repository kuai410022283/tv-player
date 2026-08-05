package api

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/bcrypt"

	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services"
	"github.com/mediaplayer/backend/internal/utils"
)

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

// ── Health Check ───────────────────────────────────────

func (h *Handler) TriggerHealthCheck(c *gin.Context) {
	var body struct {
		ExpectedMinutes int     `json:"expected_minutes"`
		IDs             []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if body.ExpectedMinutes <= 0 {
		body.ExpectedMinutes = 60 // 默认 60 分钟
	}

	if err := h.streamProxy.TriggerHealthCheck(body.ExpectedMinutes, body.IDs); err != nil {
		fail(c, 400, err.Error())
		return
	}

	ok(c, gin.H{"message": "健康检查已启动"})
}

func (h *Handler) GetHealthCheckStatus(c *gin.Context) {
	isRunning, current, total, delayMs := h.streamProxy.GetHealthCheckStatus()
	ok(c, gin.H{
		"is_running": isRunning,
		"current":    current,
		"total":      total,
		"delay_ms":   delayMs,
	})
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
	logoSvc     *services.LogoService
	syncSvc     *services.SyncService
	version     string
}

func NewHandler(channelSvc *services.ChannelService, streamProxy *services.StreamProxy, importer *services.M3UImporter, clientSvc *services.ClientService, epgSvc *services.EPGService, logoSvc *services.LogoService, syncSvc *services.SyncService, version string) *Handler {
	return &Handler{
		channelSvc:  channelSvc,
		streamProxy: streamProxy,
		importer:    importer,
		clientSvc:   clientSvc,
		epgSvc:      epgSvc,
		logoSvc:     logoSvc,
		syncSvc:     syncSvc,
		version:     version,
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

// maskProxyURL 将代理地址中的密码脱敏，例如 socks5://user:secret@host:port -> socks5://user:***@host:port
func maskProxyURL(raw string) string {
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}

// ── Groups ─────────────────────────────────────────────

func (h *Handler) ListGroups(c *gin.Context) {
	var clientID int64
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	isAdmin := c.GetString("auth_type") == "admin"
	groups, err := h.channelSvc.ListGroups(clientID, isAdmin)
	if err != nil {
		failInternal(c, err, "获取分组列表失败")
		return
	}
	// 客户端请求时，代理地址密码脱敏
	if !isAdmin {
		for i := range groups {
			groups[i].ProxyURL = maskProxyURL(groups[i].ProxyURL)
		}
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
	// 删除后重新排序，消除 sort_order 空洞
	_ = h.channelSvc.ReorderAllGroups()
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
		// 删除后重新排序，消除 sort_order 空洞
		_ = h.channelSvc.ReorderAllGroups()
	} else {
		if err := h.channelSvc.BatchUpdateGroups(req.IDs, req.Action); err != nil {
			failInternal(c, err, "批量操作失败")
			return
		}
	}
	ok(c, nil)
}

func (h *Handler) BatchSortGroups(c *gin.Context) {
	var req struct {
		Items []struct {
			ID    int64 `json:"id"`
			Order int   `json:"sort_order"`
		} `json:"items" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.BatchUpdateGroupSort(req.Items); err != nil {
		failInternal(c, err, "批量排序失败")
		return
	}
	// 分组排序变化后，连带整理频道排序数字，使其在每个分组内保持完美的 0~N 连续
	_ = h.channelSvc.ReorderChannels(0, "")
	ok(c, nil)
}

func (h *Handler) BatchSortChannels(c *gin.Context) {
	var req struct {
		Items []struct {
			ID    int64 `json:"id"`
			Order int   `json:"sort_order"`
		} `json:"items" binding:"required"`
		GroupID int64  `json:"group_id"`
		Source  string `json:"source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.BatchUpdateChannelSort(req.Items, req.GroupID, req.Source); err != nil {
		failInternal(c, err, "批量排序失败")
		return
	}
	// 频道拖拽排序（尤其是跨分组拖拽或页面全局拖拽）后，强制重新隔离计算各分组的 0~N
	_ = h.channelSvc.ReorderChannels(0, "")
	ok(c, nil)
}

func (h *Handler) GetChannelSources(c *gin.Context) {
	sources, err := h.channelSvc.GetDistinctSources()
	if err != nil {
		failInternal(c, err, "获取来源列表失败")
		return
	}
	ok(c, sources)
}

// ── Channels ───────────────────────────────────────────

func (h *Handler) ListChannels(c *gin.Context) {
	groupID, _ := strconv.ParseInt(c.Query("group_id"), 10, 64)
	search := c.Query("search")
	source := c.Query("source")
	muxSupportStr := c.Query("mux_support")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	var muxSupport *int
	if muxSupportStr != "" {
		if val, err := strconv.Atoi(muxSupportStr); err == nil {
			muxSupport = &val
		}
	}

	p := &models.PageRequest{Page: page, PageSize: pageSize}
	var clientID int64
	if id, exists := c.Get("client_id"); exists {
		clientID = id.(int64)
	}
	resp, err := h.channelSvc.ListChannels(groupID, search, source, muxSupport, p, clientID)
	if err != nil {
		failInternal(c, err, "获取频道列表失败")
		return
	}

	// 如果请求来自客户端，动态注入代理地址、解析继承防盗链头参数
	authType, _ := c.Get("auth_type")
	if authType == "client" {
		if items, ok := resp.Items.([]models.Channel); ok {
			// token 已经被去除，不在这里获取了

			strategy := h.logoSvc.GetLogoStrategy()

			// 批量获取所有频道的继承头信息（替代 N+1 查询）
			channelIDs := make([]int64, len(items))
			for i := range items {
				channelIDs[i] = items[i].ID
			}
			headerMap, _ := h.channelSvc.BatchGetInheritedHeaders(channelIDs)

			// 聚合后的列表
			groupedItems := make([]map[string]interface{}, 0)
			groupMap := make(map[string]int) // name -> index in groupedItems

			for i := range items {
				// 使用批量查询的结果
				if info, ok := headerMap[items[i].ID]; ok {
					items[i].UserAgent = info.UA
					if len(info.Headers) > 0 {
						if b, err := json.Marshal(info.Headers); err == nil {
							items[i].CustomHeaders = string(b)
						}
					} else {
						items[i].CustomHeaders = ""
					}
				}

				if items[i].EPGChannelID != "" {
					title, nextTitle, pct := h.epgSvc.GetCurrentEPGWithProgress(items[i].EPGChannelID)
					items[i].CurrentEPG = title
					items[i].NextEPG = nextTitle
					items[i].EpgPercent = pct
				} else {
					title, nextTitle, pct := h.epgSvc.GetCurrentEPGWithProgress(items[i].Name)
					items[i].CurrentEPG = title
					items[i].NextEPG = nextTitle
					items[i].EpgPercent = pct
				}

				// 开始聚合
				nameKey := items[i].Name
				var linesForThisItem []map[string]interface{}

				// 自动推断回看支持：对于 URL 匹配已知回看模式的频道，
				// 即使数据库标记为不支持也返回 true，与 generateCatchupURL 中的模式保持一致
				if !items[i].SupportCatchup {
					if canSupportCatchup(items[i].StreamURL, items[i].CatchupSource) {
						items[i].SupportCatchup = true
					}
				}

				if !items[i].IsDirect {
					rawURLs := strings.Split(items[i].StreamURL, "#")
					types := strings.Split(items[i].StreamType, "#")
					for lineIdx, u := range rawURLs {
						if strings.TrimSpace(u) == "" {
							continue
						}

						lineType := ""
						if lineIdx < len(types) {
							lineType = types[lineIdx]
						}

						ext := "ts"
						switch lineType {
						case "hls":
							ext = "m3u8"
						case "m3u8", "mp4", "flv", "mkv", "mpd":
							ext = lineType
						}

						lineProxyURL := fmt.Sprintf("/api/v1/stream/proxy/%d/line/%d/play.%s", items[i].ID, lineIdx, ext)
						linesForThisItem = append(linesForThisItem, map[string]interface{}{
							"id":              items[i].ID,
							"stream_url":      lineProxyURL,
							"stream_type":     lineType,
							"content_type":    items[i].ContentType,
							"user_agent":      items[i].UserAgent,
							"custom_headers":  items[i].CustomHeaders,
							"support_catchup": items[i].SupportCatchup,
							"catchup_days":    items[i].CatchupDays,
							"proxy_type":      items[i].ProxyType,
							"proxy_url":       maskProxyURL(items[i].ProxyURL),
						})
					}
				} else {
					// 直连模式下，把 "#" 拼接的多线路拆开下发给客户端，由客户端实现多线路容灾换线
					rawURLs := strings.Split(items[i].StreamURL, "#")
					types := strings.Split(items[i].StreamType, "#")
					for lineIdx, u := range rawURLs {
						if strings.TrimSpace(u) == "" {
							continue
						}

						lineType := ""
						if lineIdx < len(types) {
							lineType = types[lineIdx]
						}

						linesForThisItem = append(linesForThisItem, map[string]interface{}{
							"id":              items[i].ID,
							"stream_url":      strings.TrimSpace(u),
							"stream_type":     lineType,
							"content_type":    items[i].ContentType,
							"user_agent":      items[i].UserAgent,
							"custom_headers":  items[i].CustomHeaders,
							"support_catchup": items[i].SupportCatchup,
							"catchup_days":    items[i].CatchupDays,
							"proxy_type":      items[i].ProxyType,
							"proxy_url":       maskProxyURL(items[i].ProxyURL),
						})
					}
				}

				if idx, exists := groupMap[nameKey]; exists {
					// 已存在该频道，将其作为新线路追加
					lines := groupedItems[idx]["lines"].([]map[string]interface{})
					groupedItems[idx]["lines"] = append(lines, linesForThisItem...)
				} else {
					// 处理台标重写
					logoURL := h.logoSvc.ResolveLogo(items[i].Name, items[i].EPGChannelID, items[i].Logo, items[i].ID, strategy, "")

					// 新频道
					newGroup := map[string]interface{}{
						"id":          items[i].ID,
						"group_id":    items[i].GroupID,
						"name":        items[i].Name,
						"logo":        logoURL,
						"description": items[i].Description,
						"current_epg": items[i].CurrentEPG,
						"next_epg":    items[i].NextEPG,
						"epg_percent": items[i].EpgPercent,

						"sort_order":      items[i].SortOrder,
						"is_direct":       items[i].IsDirect,
						"support_catchup": items[i].SupportCatchup,
						"catchup_source":  items[i].CatchupSource,
						"catchup_days":    items[i].CatchupDays,
						"lines":           linesForThisItem,
						"proxy_type":      items[i].ProxyType,
						"proxy_url":       maskProxyURL(items[i].ProxyURL),
					}
					groupedItems = append(groupedItems, newGroup)
					groupMap[nameKey] = len(groupedItems) - 1
				}
			}
			resp.Items = groupedItems
		}
	} else {
		if items, ok := resp.Items.([]models.Channel); ok {
			strategy := h.logoSvc.GetLogoStrategy()
			for i := range items {
				items[i].Logo = h.logoSvc.ResolveLogo(items[i].Name, items[i].EPGChannelID, items[i].Logo, items[i].ID, strategy, "")
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
		// baseURL 已经被去除，全部使用相对路径
		strategy := h.logoSvc.GetLogoStrategy()
		ch.Logo = h.logoSvc.ResolveLogo(ch.Name, ch.EPGChannelID, ch.Logo, ch.ID, strategy, "")

		// token 已经被去除，不在这里获取了

		// 自动推断回看支持
		if !ch.SupportCatchup {
			if canSupportCatchup(ch.StreamURL, ch.CatchupSource) {
				ch.SupportCatchup = true
			}
		}

		if !ch.IsDirect {
			types := strings.Split(ch.StreamType, "#")
			lineType := ""
			if len(types) > 0 {
				lineType = types[0]
			}
			ext := "ts"
			switch lineType {
			case "hls":
				ext = "m3u8"
			case "m3u8", "mp4", "flv", "mkv", "mpd":
				ext = lineType
			}
			ch.StreamURL = fmt.Sprintf("/api/v1/stream/proxy/%d/line/0/play.%s", ch.ID, ext)
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
		// 代理地址密码脱敏
		ch.ProxyURL = maskProxyURL(ch.ProxyURL)
	}

	ok(c, ch)
}

func (h *Handler) CreateChannel(c *gin.Context) {
	var ch models.Channel
	if err := c.ShouldBindJSON(&ch); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	// source 默认值
	if ch.Source == "" {
		ch.Source = "手动"
	}
	// 自动分配 sort_order（追加到同分组+来源的末尾）
	if ch.SortOrder == 0 {
		ch.SortOrder = h.channelSvc.GetNextChannelSortOrder(ch.GroupID, ch.Source)
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
	_ = h.channelSvc.ReorderChannels(0, "")
	ok(c, nil)
}

func (h *Handler) BatchChannel(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.BatchDeleteChannels(req.IDs); err != nil {
		failInternal(c, err, "批量删除失败")
		return
	}
	_ = h.channelSvc.ReorderChannels(0, "")
	ok(c, nil)
}

func (h *Handler) BatchUpdateChannel(c *gin.Context) {
	var req struct {
		IDs    []int64 `json:"ids" binding:"required"`
		Action string  `json:"action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}
	if err := h.channelSvc.BatchUpdateChannels(req.IDs, req.Action); err != nil {
		failInternal(c, err, "批量操作失败")
		return
	}
	ok(c, nil)
}

// ── Stream ─────────────────────────────────────────────

func (h *Handler) ProxyStream(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	subPath := c.Param("path")

	// 提取 /line/{idx}/ 的前缀
	if strings.HasPrefix(subPath, "/line/") {
		parts := strings.SplitN(subPath[6:], "/", 2)
		if len(parts) > 0 {
			lineIdxStr := parts[0]
			// 修改请求参数，使下游依然能读到 line
			q := c.Request.URL.Query()
			q.Set("line", lineIdxStr)
			c.Request.URL.RawQuery = q.Encode()

			if len(parts) > 1 {
				subPath = "/" + parts[1]
			} else {
				subPath = "/"
			}
		}
	}

	var clientID int64
	var clientName string
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
	}
	if cname, exists := c.Get("client_name"); exists {
		clientName = cname.(string)
	}
	clientIP := utils.GetRealClientIP(c)

	var targetURL string
	if subPath != "" && subPath != "/" && !strings.HasPrefix(subPath, "/play.") {
		ch, err := h.channelSvc.GetChannel(id, 0)
		if err == nil && ch.StreamURL != "" {
			baseURLStr := h.streamProxy.GetRedirectedURL(id)
			if baseURLStr == "" {
				// 如果没有记录重定向地址，回退使用原始地址指定的线路
				rawURLs := strings.Split(ch.StreamURL, "#")
				lineIdx := 0
				if lineStr := c.Request.URL.Query().Get("line"); lineStr != "" {
					if parsedIdx, err := strconv.Atoi(lineStr); err == nil && parsedIdx >= 0 && parsedIdx < len(rawURLs) {
						lineIdx = parsedIdx
					}
				}
				baseURLStr = strings.TrimSpace(rawURLs[lineIdx])
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

	// 设备数量限制：统计套餐下 30 分钟内活跃的设备数（基于心跳）
	if clientID > 0 {
		if ms, exists := c.Get("max_streams"); exists {
			if maxStreams := ms.(int); maxStreams > 0 {
				if pid, exists := c.Get("plan_id"); exists {
					planID := pid.(int64)
					activeCount, err := h.clientSvc.CountActiveDevices(planID, clientID)
					if err == nil && activeCount >= maxStreams {
						position := h.streamProxy.EnqueueDevice(planID, clientID)
						c.JSON(429, gin.H{
							"code":           "DEVICE_LIMIT_REACHED",
							"message":        fmt.Sprintf("设备数已达上限(%d/%d)，排队中(第%d位)，请稍后重试", activeCount, maxStreams, position),
							"active_count":   activeCount,
							"max_streams":    maxStreams,
							"queue_position": position,
						})
						c.Abort()
						return
					}
					// 未超限：如果之前在排队，移出队列
					h.streamProxy.DequeueDevice(planID, clientID)
				}
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

// canSupportCatchup 根据 URL 模式自动判断频道是否支持回看。
// 与 generateCatchupURL 中的 URL 模式匹配逻辑保持一致。
func canSupportCatchup(streamURL, catchupSource string) bool {
	if catchupSource != "" {
		return true
	}
	u := streamURL
	if strings.Contains(u, "PLTV") || strings.Contains(u, "TVOD") ||
		strings.Contains(u, "itv.cmvideo.cn") || strings.Contains(u, "channel-id=") ||
		strings.Contains(u, "/live/program/live/") || strings.Contains(u, "/gitv/") ||
		strings.Contains(u, "/gitv_live/") || strings.Contains(u, "ysten-businessmobile") ||
		strings.Contains(u, "ysten-business") || strings.Contains(u, "aishang.ctlcdn") ||
		strings.Contains(u, "userid=gf001") ||
		(strings.Contains(u, "rtsp") && strings.Contains(u, "AuthInfo=")) ||
		strings.Contains(u, "/cms001/") {
		return true
	}
	return false
}

func generateSingleCatchupURL(streamURL, catchupSource string, startUnix, endUnix int64) string {
	start := time.Unix(startUnix, 0).In(time.Local)
	end := time.Unix(endUnix, 0).In(time.Local)
	durationSec := endUnix - startUnix
	if durationSec < 0 {
		durationSec = 0
	}

	if catchupSource == "" {
		u := streamURL
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		timeStr1 := start.Format("20060102150405")
		timeStr2 := end.Format("20060102150405")

		if strings.Contains(u, "PLTV") || strings.Contains(u, "TVOD") {
			if strings.Contains(u, "/PLTV/") {
				u = strings.ReplaceAll(u, "/PLTV/", "/TVOD/")
			}
			return fmt.Sprintf("%s%splayseek=%s-%s", u, sep, timeStr1, timeStr2)
		}
		if strings.Contains(u, "itv.cmvideo.cn") || strings.Contains(u, "channel-id=") {
			u = strings.ReplaceAll(u, "&livemode=1", "&livemode=4")
			u = strings.ReplaceAll(u, "000000001000", "000000002000")
			t1 := start.Format("20060102T150405.00Z")
			t2 := end.Format("20060102T150405.00Z")
			return fmt.Sprintf("%s%sstarttime=%s&endtime=%s", u, sep, t1, t2)
		}
		if strings.Contains(u, "/live/program/live/") {
			return fmt.Sprintf("%s%sstarttime=%d&endtime=%d", u, sep, startUnix, endUnix)
		}
		if strings.Contains(u, "/gitv/") {
			u = strings.ReplaceAll(u, "live1", "lookback")
			return fmt.Sprintf("%s%splayseek=%s-%s", u, sep, timeStr1, timeStr2)
		}
		if strings.Contains(u, "/gitv_live/") {
			return fmt.Sprintf("%s%sstart=%d&end=%d", u, sep, startUnix, endUnix)
		}
		if strings.Contains(u, "ysten-businessmobile") || strings.Contains(u, "ysten-business") {
			idx := strings.LastIndex(u, "/")
			if idx != -1 {
				fileName := u[idx+1:]
				base := strings.ReplaceAll(u[:idx], "live", "lookback")
				return fmt.Sprintf("%s/%s/%s/%s", base, timeStr1, timeStr2, fileName)
			}
		}
		if strings.Contains(u, "aishang.ctlcdn") {
			u = strings.ReplaceAll(u, "live", "lb")
			return fmt.Sprintf("%s%sstart=%s&end=%s", u, sep, timeStr1, timeStr2)
		}
		if strings.Contains(u, "userid=gf001") {
			t1UTC := time.Unix(startUnix, 0).UTC().Format("20060102150405")
			t2UTC := time.Unix(endUnix, 0).UTC().Format("20060102150405")
			return fmt.Sprintf("%s%sutcprogrambegin=%s&utcprogramend=%s", u, sep, t1UTC, t2UTC)
		}
		if strings.Contains(u, "rtsp") && strings.Contains(u, "AuthInfo=") {
			t1UTC := time.Unix(startUnix, 0).UTC().Format("20060102150405")
			t2UTC := time.Unix(endUnix, 0).UTC().Format("20060102150405")
			return fmt.Sprintf("%s%splayseek=%s-%s", u, sep, t1UTC, t2UTC)
		}
		if strings.Contains(u, "/cms001/") {
			t1 := start.Format("20060102T150405.00Z")
			t2 := end.Format("20060102T150405.00Z")
			return fmt.Sprintf("%s%sstarttime=%s&endtime=%s", u, sep, t1, t2)
		}

		return streamURL
	}

	source := catchupSource
	// ${} 格式（兼容旧版）
	source = strings.ReplaceAll(source, "${(b)yyyyMMddHHmmss}", start.Format("20060102150405"))
	source = strings.ReplaceAll(source, "${(e)yyyyMMddHHmmss}", end.Format("20060102150405"))
	source = strings.ReplaceAll(source, "${b}", fmt.Sprintf("%d", startUnix))
	source = strings.ReplaceAll(source, "${e}", fmt.Sprintf("%d", endUnix))
	// TIMESTAMP/TIMESTAMPL 格式（xteve 预处理后）
	source = strings.ReplaceAll(source, "TIMESTAMPL", fmt.Sprintf("%d", endUnix))
	source = strings.ReplaceAll(source, "TIMESTAMP", fmt.Sprintf("%d", startUnix))
	// {timestamp}/{utc}/{lutc}/{duration} 格式（XC/IPTV 标准）
	// 注意：{lutc} 必须在 {utc} 之前替换，否则 {lutc} 中的 utc 部分会被误替换
	source = strings.ReplaceAll(source, "{timestamp}", fmt.Sprintf("%d", startUnix))
	source = strings.ReplaceAll(source, "{lutc}", fmt.Sprintf("%d", endUnix))
	source = strings.ReplaceAll(source, "{utc}", fmt.Sprintf("%d", startUnix))
	source = strings.ReplaceAll(source, "{duration}", fmt.Sprintf("%d", durationSec))
	// 分段日期格式 {YYYY}-{MM}-{DD}--{HH}-{mm}-{ss}
	source = strings.ReplaceAll(source, "{YYYY}", start.Format("2006"))
	source = strings.ReplaceAll(source, "{MM}", start.Format("01"))
	source = strings.ReplaceAll(source, "{DD}", start.Format("02"))
	source = strings.ReplaceAll(source, "{HH}", start.Format("15"))
	source = strings.ReplaceAll(source, "{mm}", start.Format("04"))
	source = strings.ReplaceAll(source, "{ss}", start.Format("05"))
	// XC PHP 风格 {Y}-{m}-{d}:{H}-{M}-{S}
	source = strings.ReplaceAll(source, "{Y}", start.Format("2006"))
	source = strings.ReplaceAll(source, "{m}", start.Format("01"))
	source = strings.ReplaceAll(source, "{d}", start.Format("02"))
	source = strings.ReplaceAll(source, "{H}", start.Format("15"))
	source = strings.ReplaceAll(source, "{M}", start.Format("04"))
	source = strings.ReplaceAll(source, "{S}", start.Format("05"))
	// {id} 占位符（XC provider）
	if strings.Contains(source, "{id}") {
		streamID := extractStreamID(streamURL)
		source = strings.ReplaceAll(source, "{id}", streamID)
	}

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

// generateCatchupURL 包装了 generateSingleCatchupURL，支持处理带 "#" 的多线路流地址。
func generateCatchupURL(streamURL, catchupSource string, startUnix, endUnix int64) string {
	if !strings.Contains(streamURL, "#") {
		return generateSingleCatchupURL(streamURL, catchupSource, startUnix, endUnix)
	}

	rawURLs := strings.Split(streamURL, "#")
	var results []string
	for _, u := range rawURLs {
		trimmed := strings.TrimSpace(u)
		if trimmed == "" {
			continue
		}
		results = append(results, generateSingleCatchupURL(trimmed, catchupSource, startUnix, endUnix))
	}
	if len(results) == 0 {
		return streamURL
	}
	return strings.Join(results, "#")
}

// extractStreamID 从 URL 路径中提取数字 ID（用于 XC provider 的 {id} 占位符）
func extractStreamID(url string) string {
	parts := strings.Split(url, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		cleaned := strings.TrimSuffix(parts[i], ".m3u8")
		cleaned = strings.TrimSuffix(cleaned, ".ts")
		if cleaned != "" && isNumeric(cleaned) {
			return cleaned
		}
	}
	return ""
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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

	// 自动推断回看支持（与 ListChannels/GetChannel 保持一致）
	if !ch.SupportCatchup {
		if canSupportCatchup(ch.StreamURL, ch.CatchupSource) {
			ch.SupportCatchup = true
		}
	}

	if !ch.SupportCatchup {
		fail(c, 400, "该频道不支持回看")
		return
	}

	// catchup-days 范围校验（CatchupDays == 0 表示不限制）
	if ch.CatchupDays > 0 && startUnix > 0 {
		earliest := time.Now().AddDate(0, 0, -ch.CatchupDays).Unix()
		if startUnix < earliest {
			fail(c, 400, fmt.Sprintf("回看时间超出范围（仅支持最近 %d 天）", ch.CatchupDays))
			return
		}
	}

	if startUnix <= 0 || endUnix <= 0 || endUnix <= startUnix {
		fail(c, 400, "无效的回看时间范围")
		return
	}

	targetURL := generateCatchupURL(ch.StreamURL, ch.CatchupSource, startUnix, endUnix)

	if ch.IsDirect {
		// 直连模式：返回 JSON 格式的回看 URL，客户端读取后直接播放
		// 不返回 302 重定向，因为 ExoPlayer 无法处理 HTTP→RTSP/UDP/RTP 的重定向
		ok(c, gin.H{"url": targetURL})
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
	clientIP := utils.GetRealClientIP(c)

	// 设备数量限制：统计套餐下 30 分钟内活跃的设备数（基于心跳）
	if clientID > 0 {
		if ms, exists := c.Get("max_streams"); exists {
			if maxStreams := ms.(int); maxStreams > 0 {
				if pid, exists := c.Get("plan_id"); exists {
					planID := pid.(int64)
					activeCount, err := h.clientSvc.CountActiveDevices(planID, clientID)
					if err == nil && activeCount >= maxStreams {
						position := h.streamProxy.EnqueueDevice(planID, clientID)
						c.JSON(429, gin.H{
							"code":           "DEVICE_LIMIT_REACHED",
							"message":        fmt.Sprintf("设备数已达上限(%d/%d)，排队中(第%d位)，请稍后重试", activeCount, maxStreams, position),
							"active_count":   activeCount,
							"max_streams":    maxStreams,
							"queue_position": position,
						})
						c.Abort()
						return
					}
					h.streamProxy.DequeueDevice(planID, clientID)
				}
			}
		}
	}

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
	types := strings.Split(ch.StreamType, "#")
	lineType := ""
	if len(types) > 0 {
		lineType = types[0]
	}
	status, _ := h.streamProxy.CheckHealth(ch.ID, 0, ch.StreamURL, lineType)
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

// ── Playing Status ─────────────────────────────────────

func (h *Handler) UpdatePlayingStatus(c *gin.Context) {
	var req models.PlayingStatusReq
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	clientID := int64(0)
	clientIP := utils.GetRealClientIP(c)
	clientName := "Unknown"

	// 尝试从 token 提取 ClientID (如果经过 auth middleware)
	if cid, exists := c.Get("client_id"); exists {
		clientID = cid.(int64)
		if client, err := h.clientSvc.GetByID(clientID); err == nil {
			clientName = client.Name
		}
	} else {
		// 客户端请求可能没有带 auth，尝试解析 token
		token := extractToken(c)
		if token != "" {
			if client, err := h.clientSvc.GetByToken(token); err == nil {
				clientID = client.ID
				clientName = client.Name
			}
		}
	}

	if clientID == 0 {
		fail(c, 401, "未授权")
		return
	}

	// 获取频道名
	channelName := "Unknown Channel"
	if req.ChannelID > 0 {
		if ch, err := h.channelSvc.GetChannel(req.ChannelID, 0); err == nil {
			channelName = ch.Name
		}
	}

	h.streamProxy.UpdatePlayingStatus(&req, clientID, clientIP, clientName, channelName)
	ok(c, gin.H{"status": "ok"})
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

func (h *Handler) FormatSourceString(c *gin.Context) {
	var body struct {
		Content      string `json:"content"`
		TargetFormat string `json:"target_format"` // "m3u" or "txt"
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Content == "" {
		fail(c, 400, "参数错误")
		return
	}

	var formatted string
	var err error

	if body.TargetFormat == "txt" {
		formatted, err = services.FormatToTXT(body.Content)
	} else {
		// default to m3u
		formatted, err = services.FormatToM3U(body.Content)
	}

	if err != nil {
		slog.Error("Source format failed", "error", err)
		fail(c, 400, "格式化失败: "+err.Error())
		return
	}

	ok(c, gin.H{"formatted": formatted})
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
			baseURL := ""
			if val, ok := settings["server_url"]; ok && val != "" {
				baseURL = strings.TrimSuffix(val, "/")
			} else {
				scheme := "http"
				if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
					scheme = "https"
				}
				baseURL = scheme + "://" + c.Request.Host
			}
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

type updateTaskState struct {
	Status   string `json:"status"` // "downloading", "success", "error", ""
	Message  string `json:"message"`
	Progress int32  `json:"progress"`
}

var pullUpdateState atomic.Value // stores updateTaskState
var pullUpdateCancel context.CancelFunc
var pullUpdateMutex sync.Mutex

type progressWriter struct {
	total      int64
	written    int64
	basePct    int32
	fileWeight float64
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	pw.written += int64(n)
	if pw.total > 0 {
		filePct := float64(pw.written) / float64(pw.total) * 100.0
		pct := pw.basePct + int32(filePct*pw.fileWeight)
		pullUpdateState.Store(updateTaskState{Status: "downloading", Progress: pct})
	}
	return n, nil
}

func (h *Handler) PullAppUpdate(c *gin.Context) {
	var body struct {
		VersionName  string   `json:"version_name" binding:"required"`
		DownloadURL  string   `json:"download_url"`
		DownloadURLs []string `json:"download_urls"`
		UpdateLog    string   `json:"update_log"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	if body.DownloadURL == "" && len(body.DownloadURLs) == 0 {
		fail(c, 400, "缺少下载地址")
		return
	}

	urls := body.DownloadURLs
	if len(urls) == 0 {
		urls = []string{body.DownloadURL}
	}

	state := pullUpdateState.Load()
	if state != nil {
		s := state.(updateTaskState)
		if s.Status == "downloading" {
			fail(c, 400, "当前已有更新正在下载中，请稍后再试")
			return
		}
	}

	pullUpdateState.Store(updateTaskState{Status: "downloading", Progress: 0})

	pullUpdateMutex.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	pullUpdateCancel = cancel
	pullUpdateMutex.Unlock()

	go func() {
		defer func() {
			pullUpdateMutex.Lock()
			if pullUpdateCancel != nil {
				pullUpdateCancel()
				pullUpdateCancel = nil
			}
			pullUpdateMutex.Unlock()
		}()

		defer func() {
			if r := recover(); r != nil {
				pullUpdateState.Store(updateTaskState{Status: "error", Message: fmt.Sprintf("Panic: %v", r)})
			}
		}()

		var versionCode int
		// 解析 Android versionCode:
		// 按照规则 v1.1.0 -> 100, v1.0.99 -> 99，公式为 minor * 100 + patch
		reSemver := regexp.MustCompile(`(?:v)?(\d+)\.(\d+)\.(\d+)`)
		if semverMatches := reSemver.FindStringSubmatch(body.VersionName); len(semverMatches) == 4 {
			minor, _ := strconv.Atoi(semverMatches[2])
			patch, _ := strconv.Atoi(semverMatches[3])
			versionCode = minor*100 + patch
		} else {
			// 后备逻辑：提取字符串末尾的连续数字
			reFallback := regexp.MustCompile(`(\d+)$`)
			fallbackMatches := reFallback.FindStringSubmatch(body.VersionName)
			if len(fallbackMatches) > 1 {
				versionCode, _ = strconv.Atoi(fallbackMatches[1])
			}
		}

		if versionCode <= 0 {
			pullUpdateState.Store(updateTaskState{Status: "error", Message: "无法从版本号解析 versionCode: " + body.VersionName})
			return
		}

		downloadDir := filepath.Join("web", "download", fmt.Sprintf("%d_%s", versionCode, body.VersionName))
		if err := os.MkdirAll(downloadDir, 0755); err != nil {
			pullUpdateState.Store(updateTaskState{Status: "error", Message: "创建目录失败"})
			return
		}

		fileWeight := 1.0 / float64(len(urls))

		for i, u := range urls {
			basePct := int32(float64(i) / float64(len(urls)) * 100)

			parts := strings.Split(u, "/")
			filename := parts[len(parts)-1]
			if filename == "" {
				filename = fmt.Sprintf("update_%d.apk", i)
			}
			apkPath := filepath.Join(downloadDir, filename)

			skipDownload := false
			if info, err := os.Stat(apkPath); err == nil && info.Size() > 0 {
				skipDownload = true
			}

			if !skipDownload {
				req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
				if err != nil {
					pullUpdateState.Store(updateTaskState{Status: "error", Message: "创建请求失败: " + err.Error()})
					return
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						pullUpdateState.Store(updateTaskState{Status: "error", Message: "下载已取消"})
					} else {
						pullUpdateState.Store(updateTaskState{Status: "error", Message: "下载失败: " + err.Error()})
					}
					return
				}

				if resp.StatusCode != 200 {
					resp.Body.Close()
					pullUpdateState.Store(updateTaskState{Status: "error", Message: fmt.Sprintf("下载失败: HTTP %d", resp.StatusCode)})
					return
				}

				tmpPath := apkPath + ".tmp"
				out, err := os.Create(tmpPath)
				if err != nil {
					resp.Body.Close()
					pullUpdateState.Store(updateTaskState{Status: "error", Message: "创建文件失败: " + err.Error()})
					return
				}

				pw := &progressWriter{
					total:      resp.ContentLength,
					basePct:    basePct,
					fileWeight: fileWeight,
				}
				src := io.TeeReader(resp.Body, pw)

				if _, err := io.Copy(out, src); err != nil {
					_ = out.Close()
					_ = resp.Body.Close()
					_ = os.Remove(tmpPath)
					pullUpdateState.Store(updateTaskState{Status: "error", Message: "保存文件失败: " + err.Error()})
					return
				}
				_ = out.Close()
				resp.Body.Close()

				if err := os.Rename(tmpPath, apkPath); err != nil {
					_ = os.Remove(tmpPath)
					pullUpdateState.Store(updateTaskState{Status: "error", Message: "重命名文件失败: " + err.Error()})
					return
				}
			}
		}

		logPath := filepath.Join(downloadDir, "version.txt")
		_ = os.WriteFile(logPath, []byte(body.UpdateLog), 0644)

		_ = h.channelSvc.SetSetting("update_version_code", strconv.Itoa(versionCode))
		_ = h.channelSvc.SetSetting("update_version_name", body.VersionName)
		_ = h.channelSvc.SetSetting("update_log", body.UpdateLog)
		_ = h.channelSvc.SetSetting("update_download_url", "")
		_ = h.channelSvc.SetSetting("update_force", "false")

		pullUpdateState.Store(updateTaskState{Status: "success", Message: "拉取并发布成功", Progress: 100})
	}()

	ok(c, gin.H{"message": "已开始在后台下载"})
}

func (h *Handler) PullAppUpdateProgress(c *gin.Context) {
	state := pullUpdateState.Load()
	if state == nil {
		ok(c, gin.H{"status": "", "progress": 0, "message": ""})
		return
	}
	ok(c, state)
}

func (h *Handler) CancelPullAppUpdate(c *gin.Context) {
	pullUpdateMutex.Lock()
	defer pullUpdateMutex.Unlock()

	if pullUpdateCancel != nil {
		pullUpdateCancel()
		pullUpdateCancel = nil
		ok(c, gin.H{"message": "已发送取消指令"})
		return
	}
	fail(c, 400, "当前没有正在进行的下载")
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
		slog.Warn("admin login failed", "ip", utils.GetRealClientIP(c), "err", err)
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

// GetAdminConfig 返回管理后台的基础公开配置状态
func (h *Handler) GetAdminConfig(c *gin.Context) {
	hash, err := h.getAdminPasswordHash()
	if err != nil {
		failInternal(c, err, "获取配置失败")
		return
	}

	// 判断当前密码是否依然是默认密码 "admin123"
	isDefault := bcrypt.CompareHashAndPassword([]byte(hash), []byte("admin123")) == nil

	ok(c, gin.H{
		"is_default_password": isDefault,
	})
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
		fail(c, 400, "原密码错误")
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
	totalResp, _ := h.channelSvc.ListChannels(0, "", "", nil, p, 0)
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
		"version":    h.version,
		"go_version": runtime.Version(),
		"started_at": startTime.Format(time.RFC3339),
	})
}

// ── Logo Management ───────────────────────────────────────

func (h *Handler) GetLogo(c *gin.Context) {
	name := c.Query("name")
	idStr := c.Query("id")
	if name == "" {
		c.Status(http.StatusNoContent)
		return
	}

	if name == "default" || name == "default.png" {
		c.Header("Cache-Control", "public, max-age=604800")
		c.File("./library/channel_logo/default.png")
		return
	}

	strategy := h.logoSvc.GetLogoStrategy()

	var id int64
	if idStr != "" {
		id, _ = strconv.ParseInt(idStr, 10, 64)
	}

	cleanName := h.logoSvc.CleanName(name)

	var dbLogo, chName, epgID string
	if id > 0 {
		if ch, err := h.channelSvc.GetChannel(id, 0); err == nil && ch != nil {
			dbLogo = ch.Logo
			chName = ch.Name
			epgID = ch.EPGChannelID
		}
	}

	var cleanEPG string
	if epgID != "" {
		cleanEPG = h.logoSvc.CleanName(epgID)
	}

	serveLocal := func() bool {
		if cleanEPG != "" && h.logoSvc.HasLocalLogo(cleanEPG) {
			c.Header("Cache-Control", "public, max-age=604800")
			c.File(h.logoSvc.GetLogoPath(cleanEPG))
			return true
		}
		if cleanName != "" && h.logoSvc.HasLocalLogo(cleanName) {
			c.Header("Cache-Control", "public, max-age=604800")
			c.File(h.logoSvc.GetLogoPath(cleanName))
			return true
		}
		if chName != "" {
			cleanChName := h.logoSvc.CleanName(chName)
			if h.logoSvc.HasLocalLogo(cleanChName) {
				c.Header("Cache-Control", "public, max-age=604800")
				c.File(h.logoSvc.GetLogoPath(cleanChName))
				return true
			}
		}
		return false
	}

	redirectDB := func() bool {
		if dbLogo != "" {
			c.Header("Cache-Control", "public, max-age=604800")
			c.Redirect(http.StatusMovedPermanently, dbLogo)
			return true
		}
		return false
	}

	switch strategy {
	case "local":
		if serveLocal() {
			return
		}
		if redirectDB() {
			return
		}
	case "source":
		if redirectDB() {
			return
		}
		if serveLocal() {
			return
		}
	case "interface":
		if redirectDB() {
			return
		}
		if serveLocal() {
			return
		}
	}

	// Fallback to default.png
	c.Header("Cache-Control", "public, max-age=604800")
	c.File("./library/channel_logo/default.png")
}

func (h *Handler) TriggerCacheLogos(c *gin.Context) {
	go h.logoSvc.CacheExistingLogos()
	ok(c, gin.H{"message": "已在后台启动缓存现有台标任务"})
}

func (h *Handler) TriggerBatchFetchLogos(c *gin.Context) {
	var req struct {
		Overwrite bool `json:"overwrite"`
	}
	_ = c.ShouldBindJSON(&req)
	go h.logoSvc.FetchLogosFromSources(req.Overwrite)
	ok(c, gin.H{"message": "已在后台启动从源库批量拉取任务"})
}

// ── Sync ───────────────────────────────────────────────

func (h *Handler) GetDBSnapshot(c *gin.Context) {
	// Verify sync_serve_token
	serveToken, _ := h.channelSvc.GetSetting("sync_serve_token")
	reqToken := c.Query("token")
	if reqToken == "" {
		reqToken = c.GetHeader("Authorization")
		reqToken = strings.TrimPrefix(reqToken, "Bearer ")
	}

	if serveToken == "" || reqToken != serveToken {
		fail(c, 401, "unauthorized sync access")
		return
	}

	tempPath := filepath.Join("data", "backup_temp.db")
	if err := h.syncSvc.Snapshot(tempPath); err != nil {
		failInternal(c, err, "snapshot failed")
		return
	}

	defer func() { _ = os.Remove(tempPath) }()

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Transfer-Encoding", "binary")
	c.Header("Content-Disposition", "attachment; filename=snapshot.db")
	c.Header("Content-Type", "application/octet-stream")
	c.File(tempPath)
}

func (h *Handler) GetLogosSnapshot(c *gin.Context) {
	// Verify sync_serve_token
	serveToken, _ := h.channelSvc.GetSetting("sync_serve_token")
	reqToken := c.Query("token")
	if reqToken == "" {
		reqToken = c.GetHeader("Authorization")
		reqToken = strings.TrimPrefix(reqToken, "Bearer ")
	}

	if serveToken == "" || reqToken != serveToken {
		fail(c, 401, "unauthorized sync access")
		return
	}

	dir := "./library/channel_logo"
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			ok(c, gin.H{"logos": map[string]string{}})
			return
		}
		failInternal(c, err, "failed to read logo directory")
		return
	}

	snapshots := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		f, err := os.Open(filePath)
		if err != nil {
			continue
		}

		h := md5.New()
		if _, err := io.Copy(h, f); err == nil {
			snapshots[entry.Name()] = fmt.Sprintf("%x", h.Sum(nil))
		}
		_ = f.Close()
	}

	ok(c, gin.H{"logos": snapshots})
}

func (h *Handler) SyncFromMaster(c *gin.Context) {
	var req struct {
		MasterURL   string `json:"master_url" binding:"required"`
		MasterToken string `json:"master_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "invalid parameters")
		return
	}

	if err := h.syncSvc.SyncFromMaster(req.MasterURL, req.MasterToken); err != nil {
		failInternal(c, err, "sync from master failed: "+err.Error())
		return
	}

	ok(c, gin.H{"message": "同步成功"})
}

func (h *Handler) PingMaster(c *gin.Context) {
	var req struct {
		MasterURL string `json:"master_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "invalid parameters")
		return
	}

	// Remove trailing slash if any
	masterURL := req.MasterURL
	for len(masterURL) > 0 && masterURL[len(masterURL)-1] == '/' {
		masterURL = masterURL[:len(masterURL)-1]
	}

	apiURL := masterURL + "/ping"
	httpReq, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		fail(c, 400, "failed to create request: "+err.Error())
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		fail(c, 400, "主节点连接失败，请检查地址是否正确或网络是否畅通")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fail(c, 400, fmt.Sprintf("主节点返回异常状态码: %d", resp.StatusCode))
		return
	}

	ok(c, gin.H{"message": "连接成功"})
}

func (h *Handler) MirrorChannel(c *gin.Context) {
	var req struct {
		SourceChannelID int64  `json:"source_channel_id" binding:"required"`
		TargetGroupID   int64  `json:"target_group_id" binding:"required"`
		TargetSource    string `json:"target_source" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, 400, "参数错误")
		return
	}

	ch, err := h.channelSvc.MirrorChannel(req.SourceChannelID, req.TargetGroupID, req.TargetSource)
	if err != nil {
		fail(c, 500, err.Error())
		return
	}

	ok(c, ch)
}
