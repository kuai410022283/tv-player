package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services"
)

type PlanHandler struct {
	svc *services.PlanService
}

func NewPlanHandler(svc *services.PlanService) *PlanHandler {
	return &PlanHandler{svc: svc}
}

func (h *PlanHandler) GetPlans(c *gin.Context) {
	search := c.Query("search")
	items, err := h.svc.GetPlans(search)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: items})
}

func (h *PlanHandler) AddPlan(c *gin.Context) {
	var m models.SubscriptionPlan
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid request"})
		return
	}
	if m.Name == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "name is required"})
		return
	}

	if err := h.svc.AddPlan(&m); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: m})
}

func (h *PlanHandler) UpdatePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid id"})
		return
	}
	var m models.SubscriptionPlan
	if err := c.ShouldBindJSON(&m); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid request"})
		return
	}
	m.ID = id
	if m.Name == "" {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "name is required"})
		return
	}

	if err := h.svc.UpdatePlan(&m); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success", Data: m})
}

func (h *PlanHandler) DeletePlan(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse{Code: -1, Message: "invalid id"})
		return
	}

	if err := h.svc.DeletePlan(id); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse{Code: -1, Message: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse{Code: 0, Message: "success"})
}

func (h *PlanHandler) GetSubscription(c *gin.Context) {
	planName := c.Query("subscription_plans")
	token := c.Query("subscription_token")
	format := c.Query("subscription_format")

	if !h.svc.IsExternalSubEnabled() {
		c.String(http.StatusForbidden, "Forbidden: External subscription is disabled")
		return
	}

	if planName == "" || token == "" {
		c.String(http.StatusBadRequest, "Missing subscription_plans or subscription_token")
		return
	}

	channels, err := h.svc.GetSubscriptionChannels(planName, token)
	if err != nil {
		c.String(http.StatusForbidden, "Forbidden: %v", err)
		return
	}

	// 自动推断回看支持（与 ListChannels/GetChannel 保持一致）
	for _, ch := range channels {
		if !ch.SupportCatchup {
			if canSupportCatchup(ch.StreamURL, ch.CatchupSource) {
				ch.SupportCatchup = true
			}
		}
	}

	// 确定代理及台标的 baseURL
	baseURL := strings.TrimSuffix(h.svc.GetServerURL(), "/")
	if baseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, c.Request.Host)
	}

	type mergedLine struct {
		URL  string
		Type string
	}
	type mergedChannel struct {
		*models.SubscriptionChannel
		AllLines []mergedLine
	}
	type mergedGroup struct {
		GroupName string
		Channels  []*mergedChannel
	}
	var groupList []*mergedGroup
	groupMap := make(map[string]int) // map[GroupName] -> index in groupList
	channelMap := make(map[string]map[string]int) // map[GroupName] -> map[ChannelName] -> index in groupList[idx].Channels

	for _, ch := range channels {
		if _, ok := groupMap[ch.GroupName]; !ok {
			groupList = append(groupList, &mergedGroup{GroupName: ch.GroupName})
			groupMap[ch.GroupName] = len(groupList) - 1
			channelMap[ch.GroupName] = make(map[string]int)
		}
		
		lines := strings.Split(ch.StreamURL, "#")
		types := strings.Split(ch.StreamType, "#")
		
		var currentLines []mergedLine
		for i, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				t := ""
				if i < len(types) {
					t = types[i]
				}
				currentLines = append(currentLines, mergedLine{URL: l, Type: t})
			}
		}

		gIdx := groupMap[ch.GroupName]
		if cIdx, exists := channelMap[ch.GroupName][ch.Name]; exists {
			groupList[gIdx].Channels[cIdx].AllLines = append(groupList[gIdx].Channels[cIdx].AllLines, currentLines...)
		} else {
			mc := &mergedChannel{SubscriptionChannel: ch, AllLines: currentLines}
			groupList[gIdx].Channels = append(groupList[gIdx].Channels, mc)
			channelMap[ch.GroupName][ch.Name] = len(groupList[gIdx].Channels) - 1
		}
	}

	// TXT 格式
	if format == "txt" {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		if c.Query("download") == "1" {
			c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.txt"`, planName))
		}
		var sb strings.Builder

		for _, grp := range groupList {
			sb.WriteString(fmt.Sprintf("%s,#genre#\n", grp.GroupName))
			for _, ch := range grp.Channels {

			for lineIdx, ml := range ch.AllLines {
				displayName := ch.Name

				var playURL string
				if ch.IsDirect {
					playURL = ml.URL
				} else {
					ext := "ts"
					switch ml.Type {
					case "hls", "":
						ext = "m3u8"
					case "mp4", "flv", "mkv", "mpd":
						ext = ml.Type
					}
					playURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d/play.%s?line=%d&token=%s&plan=1", baseURL, ch.ID, ext, lineIdx, token)
				}

				sb.WriteString(fmt.Sprintf("%s,%s\n", displayName, playURL))
			}
		}
		}
		c.String(http.StatusOK, sb.String())
		return
	}

	// M3U 格式 (默认)
	userAgent := c.Request.UserAgent()
	isBrowser := strings.Contains(userAgent, "Mozilla/") || strings.Contains(userAgent, "Chrome/") || strings.Contains(userAgent, "Safari/")

	if c.Query("download") == "1" {
		c.Header("Content-Type", "application/x-mpegurl; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.m3u"`, planName))
	} else if c.Query("preview") == "1" || isBrowser {
		c.Header("Content-Type", "text/plain; charset=utf-8")
	} else {
		c.Header("Content-Type", "application/x-mpegurl; charset=utf-8")
	}
	var sb strings.Builder
	sb.WriteString("#EXTM3U")

	epgURLs := h.svc.GetEPGSourceURL()
	if epgURLs != "" {
		epgURLs = strings.ReplaceAll(epgURLs, "\r\n", ",")
		epgURLs = strings.ReplaceAll(epgURLs, "\n", ",")
		epgURLs = strings.Trim(epgURLs, ",")
		if epgURLs != "" {
			sb.WriteString(fmt.Sprintf(` x-tvg-url="%s"`, epgURLs))
		}
	}

	shift := h.svc.GetEPGTimeShift()
	if shift != 0 {
		sb.WriteString(fmt.Sprintf(` tvg-shift="%d"`, shift))
	}
	sb.WriteString("\n")

	logoSvc := h.svc.GetLogoService()
	strategy := "source"
	if logoSvc != nil {
		strategy = logoSvc.GetLogoStrategy()
	}

	var chanIndex int
	for _, grp := range groupList {
		for _, ch := range grp.Channels {
			chanIndex++
		logoURL := ch.Logo
		if logoSvc != nil {
			logoURL = logoSvc.ResolveLogo(ch.Name, ch.EPGChannelID, ch.Logo, ch.ID, strategy, baseURL)
		} else {
			if logoURL == "" {
				logoURL = fmt.Sprintf("%s/api/v1/logo?name=%s", baseURL, url.QueryEscape(ch.Name))
			} else if !strings.HasPrefix(logoURL, "http://") && !strings.HasPrefix(logoURL, "https://") {
				logoURL = baseURL + logoURL
			}
		}

		if strings.HasPrefix(logoURL, baseURL) {
			if strings.Contains(logoURL, "?") {
				logoURL = fmt.Sprintf("%s&token=%s&plan=1", logoURL, token)
			} else {
				logoURL = fmt.Sprintf("%s?token=%s&plan=1", logoURL, token)
			}
		}

		catchupStr := ""
		if ch.SupportCatchup {
			catchupType := ch.CatchupType
			if catchupType == "" {
				catchupType = "default"
			}
			catchupStr = fmt.Sprintf(` catchup="%s" catchup-days="%d"`, catchupType, ch.CatchupDays)
			if ch.CatchupSource != "" {
				catchupStr += fmt.Sprintf(` catchup-source="%s"`, ch.CatchupSource)
			}
		}

		tvgID := ch.EPGChannelID
		if tvgID == "" {
			tvgID = fmt.Sprintf("%d", ch.ID)
		}

		contentTypeStr := ""
		if ch.ContentType != "" {
			contentTypeStr = fmt.Sprintf(` tvg-type="%s"`, ch.ContentType)
		}

		for lineIdx, ml := range ch.AllLines {
			displayName := ch.Name
			var playURL string
			if ch.IsDirect {
				playURL = ml.URL
				var kodiHeaders []string
				if ch.UserAgent != "" {
					kodiHeaders = append(kodiHeaders, "User-Agent="+ch.UserAgent)
				}
				if ch.CustomHeaders != "" {
					var headersMap map[string]string
					if err := json.Unmarshal([]byte(ch.CustomHeaders), &headersMap); err == nil {
						for k, v := range headersMap {
							if strings.ToLower(k) != "user-agent" {
								kodiHeaders = append(kodiHeaders, fmt.Sprintf("%s=%s", k, v))
							}
						}
					}
				}
				if len(kodiHeaders) > 0 {
					playURL = playURL + "|" + strings.Join(kodiHeaders, "&")
				}
			} else {
				ext := "ts"
				switch ml.Type {
				case "hls", "":
					ext = "m3u8"
				case "mp4", "flv", "mkv", "mpd":
					ext = ml.Type
				}
				playURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d/play.%s?line=%d&token=%s&plan=1", baseURL, ch.ID, ext, lineIdx, token)
			}

			tvgChno := chanIndex
			sb.WriteString(fmt.Sprintf(`#EXTINF:-1 tvg-id="%s" tvg-chno="%d" tvg-name="%s" tvg-logo="%s" group-title="%s"%s%s,%s`+"\n",
				tvgID, tvgChno, ch.Name, logoURL, ch.GroupName, catchupStr, contentTypeStr, displayName))
			sb.WriteString(playURL)
			sb.WriteString("\n")
		}
		}
	}
	c.String(http.StatusOK, sb.String())
}
