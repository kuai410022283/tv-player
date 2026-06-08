package api

import (
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

	// 确定代理及台标的 baseURL
	baseURL := strings.TrimSuffix(h.svc.GetServerURL(), "/")
	if baseURL == "" {
		scheme := "http"
		if c.Request.TLS != nil || c.Request.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, c.Request.Host)
	}

	// TXT 格式
	if format == "txt" {
		c.Header("Content-Type", "text/plain; charset=utf-8")
		var sb strings.Builder

		currentGroup := ""
		for _, ch := range channels {
			if ch.GroupName != currentGroup {
				currentGroup = ch.GroupName
				sb.WriteString(fmt.Sprintf("%s,#genre#\n", currentGroup))
			}

			lines := strings.Split(ch.StreamURL, "#")
			for lineIdx, u := range lines {
				u = strings.TrimSpace(u)
				if u == "" {
					continue
				}

				displayName := ch.Name

				var playURL string
				if ch.IsDirect {
					playURL = u
				} else {
					ext := "ts"
					switch ch.StreamType {
					case "hls", "":
						ext = "m3u8"
					case "mp4", "flv", "mkv", "mpd":
						ext = ch.StreamType
					}
					playURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d/play.%s?line=%d&token=%s", baseURL, ch.ID, ext, lineIdx, token)
				}

				sb.WriteString(fmt.Sprintf("%s,%s\n", displayName, playURL))
			}
		}
		c.String(http.StatusOK, sb.String())
		return
	}

	// M3U 格式 (默认)
	c.Header("Content-Type", "application/x-mpegurl; charset=utf-8")
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")

	for _, ch := range channels {
		logoURL := ch.Logo
		if logoURL == "" {
			logoURL = fmt.Sprintf("%s/api/v1/logo?name=%s", baseURL, url.QueryEscape(ch.Name))
		} else if !strings.HasPrefix(logoURL, "http://") && !strings.HasPrefix(logoURL, "https://") {
			logoURL = baseURL + logoURL
		}

		catchupStr := ""
		if ch.SupportCatchup {
			catchupType := ch.CatchupType
			if catchupType == "" {
				catchupType = "default"
			}
			catchupStr = fmt.Sprintf(` catchup="%s" catchup-days="%d"`, catchupType, ch.CatchupDays)
		}

		tvgID := ch.EPGChannelID
		if tvgID == "" {
			tvgID = fmt.Sprintf("%d", ch.ID)
		}

		lines := strings.Split(ch.StreamURL, "#")
		for lineIdx, u := range lines {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}

			displayName := ch.Name

			var playURL string
			if ch.IsDirect {
				playURL = u
			} else {
				ext := "ts"
				switch ch.StreamType {
				case "hls", "":
					ext = "m3u8"
				case "mp4", "flv", "mkv", "mpd":
					ext = ch.StreamType
				}
				playURL = fmt.Sprintf("%s/api/v1/stream/proxy/%d/play.%s?line=%d&token=%s", baseURL, ch.ID, ext, lineIdx, token)
			}

			sb.WriteString(fmt.Sprintf(`#EXTINF:-1 tvg-id="%s" tvg-name="%s" tvg-logo="%s" group-title="%s"%s,%s`+"\n",
				tvgID, ch.Name, logoURL, ch.GroupName, catchupStr, displayName))
			sb.WriteString(playURL + "\n")
		}
	}
	c.String(http.StatusOK, sb.String())
}
