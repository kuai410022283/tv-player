package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/models"
)

// StreamProxy manages proxied streams with health checking
type StreamProxy struct {
	cfg            *config.StreamConfig
	mu             sync.RWMutex
	streams        map[string]*models.ActiveStream
	cancels        map[string]context.CancelFunc
	redirectedURLs map[int64]string // 存储每个频道的重定向后基础 URL
	client         *http.Client
	channelSvc     *ChannelService
	sem            chan struct{} // 并发控制
}

// removed streamState

func NewStreamProxy(cfg *config.StreamConfig, channelSvc *ChannelService) *StreamProxy {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 50
	}
	sp := &StreamProxy{
		cfg:            cfg,
		streams:        make(map[string]*models.ActiveStream),
		cancels:        make(map[string]context.CancelFunc),
		redirectedURLs: make(map[int64]string),
		channelSvc:     channelSvc,
		client: &http.Client{
			// 不设置全局 Timeout（长流会被中断），但限制连接建立和响应头超时
			Transport: &http.Transport{
				ResponseHeaderTimeout: 30 * time.Second, // 等待上游响应头的最长时间
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
		},
		sem: make(chan struct{}, maxConcurrent),
	}
	os.MkdirAll(cfg.CacheDir, 0755)
	return sp
}

// SetRedirectedURL 存储频道的重定向 URL
func (sp *StreamProxy) SetRedirectedURL(channelID int64, url string) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	sp.redirectedURLs[channelID] = url
}

// GetRedirectedURL 获取频道重定向后的基础 URL
func (sp *StreamProxy) GetRedirectedURL(channelID int64) string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.redirectedURLs[channelID]
}

// CheckHealth verifies a stream URL is reachable and returns stream info
func (sp *StreamProxy) CheckHealth(channelID int64, rawURL, streamType string) (*models.StreamStatus, error) {
	// 如果是多源合并（#拼接），取第一条线路进行探测
	urls := strings.Split(rawURL, "#")
	if len(urls) == 0 || urls[0] == "" {
		return &models.StreamStatus{URL: rawURL, Status: "error", ErrorMsg: "空地址"}, nil
	}
	url := strings.TrimSpace(urls[0])

	status := &models.StreamStatus{
		URL:    url,
		Status: "unknown",
	}

	// 校验 URL
	if err := ValidateStreamURL(url); err != nil {
		status.Status = "error"
		status.ErrorMsg = "URL 不安全: " + err.Error()
		return status, err
	}

	// 获取自定义的 User-Agent 和 Headers
	ua, headers, _ := sp.channelSvc.GetInheritedHeaders(channelID)
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
	}

	// 健康检查用独立短超时 client，禁用 KeepAlive 避免关闭未读完的响应体导致闲置连接接收到乱码
	healthClient := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	switch streamType {
	case "hls", "mp4", "dash", "flv", "ts":
		// 很多 IPTV 服务端会拦截 HEAD 请求 (返回 405/403)，因此改用 GET，并加入基础 UA 伪装
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			status.Status = "error"
			status.ErrorMsg = err.Error()
			return status, err
		}
		req.Header.Set("User-Agent", ua)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		// 显式告诉服务端不保持连接，配合 DisableKeepAlives 彻底断开
		req.Close = true
		
		resp, err := healthClient.Do(req)
		if err != nil {
			status.Status = "error"
			status.ErrorMsg = err.Error()
			return status, err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			status.Status = "online"
		} else {
			status.Status = "offline"
			status.ErrorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	case "rtmp", "rtsp":
		// For RTMP/RTSP, we just try a TCP connection
		status.Status = "online" // simplified; real impl would use ffmpeg probe
	default:
		status.Status = "unknown"
	}
	return status, nil
}

// StartHealthCheck runs periodic health checks on all channels
func (sp *StreamProxy) StartHealthCheck(stop <-chan struct{}) {
	interval := time.Duration(sp.cfg.HealthCheckSec) * time.Second
	if interval < 10 { interval = 10 * time.Second }

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sp.checkAllChannels()
		}
	}
}

func (sp *StreamProxy) checkAllChannels() {
	page := 1
	pageSize := 50
	maxPages := 10
	for page <= maxPages {
		p := &models.PageRequest{Page: page, PageSize: pageSize}
		resp, err := sp.channelSvc.ListChannels(0, "", p, 0)
		if err != nil || resp == nil {
			break
		}

		channels, ok := resp.Items.([]models.Channel)
		if !ok || len(channels) == 0 {
			break
		}

		for _, ch := range channels {
			status, _ := sp.CheckHealth(ch.ID, ch.StreamURL, ch.StreamType)
			newStatus := "offline"
			if status.Status == "online" {
				newStatus = "online"
			}
			_ = sp.channelSvc.UpdateStatus(ch.ID, newStatus)
		}

		if len(channels) < pageSize {
			break
		}
		page++
	}
}

// GetProxyURL returns the proxied URL for a channel
func (sp *StreamProxy) GetProxyURL(channelID int64, baseURL string) string {
	return fmt.Sprintf("%s/api/v1/stream/proxy/%d", baseURL, channelID)
}

// ServeStream proxies the actual stream data. If targetURL is provided, it proxies that URL instead of the channel's default StreamURL.
func (sp *StreamProxy) ServeStream(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, targetURL string) error {
	// 并发控制
	select {
	case sp.sem <- struct{}{}:
		defer func() { <-sp.sem }()
	default:
		return fmt.Errorf("并发流数已达上限 (%d)", sp.cfg.MaxConcurrent)
	}

	ch, err := sp.channelSvc.GetChannel(channelID, 0)
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	streamToProxy := ch.StreamURL
	if targetURL != "" {
		streamToProxy = targetURL
	}

	rawURLs := strings.Split(streamToProxy, "#")
	var resp *http.Response
	var finalURL string
	var lastErr error

	ctx, cancel := context.WithCancel(r.Context())

	// Apply inherited UA and custom headers
	ua, headers, err := sp.channelSvc.GetInheritedHeaders(channelID)
	if err != nil {
		ua = "MediaPlayer/1.0"
	}

	for _, u := range rawURLs {
		u = strings.TrimSpace(u)
		if u == "" { continue }

		// 校验流地址，防止 SSRF
		if err := ValidateStreamURL(u); err != nil {
			lastErr = err
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		
		req.Header.Set("User-Agent", ua)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err = sp.client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 400 {
			finalURL = u
			if resp.Request != nil && resp.Request.URL != nil {
				finalURL = resp.Request.URL.String()
			}
			contentType := resp.Header.Get("Content-Type")
			isM3U8 := strings.Contains(strings.ToLower(contentType), "mpegurl") ||
				strings.Contains(strings.ToLower(resp.Request.URL.Path), ".m3u8") ||
				strings.Contains(strings.ToLower(u), ".m3u8")
			if isM3U8 {
				sp.SetRedirectedURL(channelID, finalURL)
			}
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("status code %d", resp.StatusCode)
		}
	}

	if finalURL == "" {
		cancel()
		if lastErr != nil {
			return fmt.Errorf("所有线路均失效, 最后错误: %w", lastErr)
		}
		return fmt.Errorf("无有效播放线路")
	}

	sessionID := fmt.Sprintf("%d-%d-%d", channelID, clientID, time.Now().UnixNano())

	// Update stream state
	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		SessionID:   sessionID,
		ChannelID:   channelID,
		ChannelName: ch.Name,
		ClientID:    clientID,
		ClientName:  clientName,
		ClientIP:    clientIP,
		URL:         finalURL,
		Status:      "playing",
		StartedAt:   time.Now(),
		LastActive:  time.Now(),
	}
	sp.cancels[sessionID] = cancel
	sp.mu.Unlock()

	defer func() {
		cancel()
		sp.mu.Lock()
		delete(sp.streams, sessionID)
		delete(sp.cancels, sessionID)
		sp.mu.Unlock()
	}()

	defer resp.Body.Close()

	// Copy headers
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream with buffering
	buf := make([]byte, sp.cfg.BufferSize)
	reader := bufio.NewReaderSize(resp.Body, sp.cfg.BufferSize)
	
	lastUpdate := time.Now()
	var bytesSinceLastUpdate int64 = 0

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			
			bytesSinceLastUpdate += int64(n)
			now := time.Now()
			
			if now.Sub(lastUpdate) >= time.Second {
				sp.mu.Lock()
				if s, ok := sp.streams[sessionID]; ok {
					s.LastActive = now
					s.SpeedBytes = bytesSinceLastUpdate
				}
				sp.mu.Unlock()
				bytesSinceLastUpdate = 0
				lastUpdate = now
			}
		}
		if err != nil {
			if err == io.EOF { return nil }
			return err
		}
	}
}

// KillStream forces a specific proxy stream to disconnect
func (sp *StreamProxy) KillStream(sessionID string) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if cancel, ok := sp.cancels[sessionID]; ok {
		cancel()
		return true
	}
	return false
}

// GetActiveStreams returns currently active stream states
func (sp *StreamProxy) GetActiveStreams() []models.ActiveStream {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	var streams []models.ActiveStream
	for _, s := range sp.streams {
		streams = append(streams, *s)
	}
	return streams
}

// M3U parsing
func extractAttr(line, attr string) string {
	prefix := attr + "=\""
	if start := strings.Index(line, prefix); start >= 0 {
		start += len(prefix)
		if end := strings.Index(line[start:], "\""); end >= 0 {
			return line[start : start+end]
		}
	}
	return ""
}

func ParseM3U(reader io.Reader) ([]map[string]string, error) {
	scanner := bufio.NewScanner(reader)
	// 增加 buffer 限制，防止某些 M3U 文件单行过长（如带有大尺寸 base64 logo url 时）超过 64KB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)
	var channels []map[string]string
	var current map[string]string

	var globalCatchupType string
	var globalCatchupSource string
	var globalCatchupDays string

	isFirstLine := true
	currentTxtGroup := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if isFirstLine {
			// 移除 UTF-8 BOM，防止 strings.HasPrefix("#EXTM3U") 匹配失败
			line = strings.TrimPrefix(line, "\xef\xbb\xbf")
			isFirstLine = false
		}
		if line == "" { continue }

		if strings.HasPrefix(line, "#EXTM3U") {
			globalCatchupType = extractAttr(line, "catchup")
			globalCatchupSource = extractAttr(line, "catchup-source")
			globalCatchupDays = extractAttr(line, "catchup-days")
		} else if strings.HasPrefix(line, "#EXTINF:") {
			current = parseExtInf(line)
			if current["catchup"] == "" && globalCatchupType != "" {
				current["catchup"] = globalCatchupType
			}
			if current["catchup-source"] == "" && globalCatchupSource != "" {
				current["catchup-source"] = globalCatchupSource
			}
			if current["catchup-days"] == "" && globalCatchupDays != "" {
				current["catchup-days"] = globalCatchupDays
			}
		} else if strings.HasPrefix(line, "#EXTVLCOPT:") && current != nil {
			parseVlcOpt(line, current)
		} else if !strings.HasPrefix(line, "#") {
			if current != nil {
				current["url"] = line
				channels = append(channels, current)
				current = nil
			} else {
				// 兼容 TXT 格式解析 (Name,URL 或 GroupName,#genre#)
				parts := strings.SplitN(line, ",", 2)
				if len(parts) == 2 {
					name := strings.TrimSpace(parts[0])
					url := strings.TrimSpace(parts[1])
					if url == "#genre#" {
						currentTxtGroup = name
					} else if name != "" && url != "" {
						ch := make(map[string]string)
						ch["name"] = name
						ch["url"] = url
						if currentTxtGroup != "" {
							ch["group-title"] = currentTxtGroup
						}
						channels = append(channels, ch)
					}
				}
			}
		}
	}
	return channels, scanner.Err()
}

func parseVlcOpt(line string, ch map[string]string) {
	opt := strings.TrimPrefix(line, "#EXTVLCOPT:")
	parts := strings.SplitN(opt, "=", 2)
	if len(parts) != 2 {
		return
	}
	key := strings.TrimSpace(parts[0])
	val := strings.TrimSpace(parts[1])

	switch key {
	case "http-user-agent":
		ch["user_agent"] = val
	case "http-referrer":
		ch["http-referrer"] = val
	case "http-origin":
		ch["http-origin"] = val
	}
}

func parseExtInf(line string) map[string]string {
	ch := make(map[string]string)
	ch["raw"] = line

	// Extract name (after last comma)
	if idx := strings.LastIndex(line, ","); idx >= 0 {
		ch["name"] = strings.TrimSpace(line[idx+1:])
	}

	// Extract attributes
	attrs := []string{"tvg-id", "tvg-name", "tvg-logo", "group-title", "tvg-chno", "catchup", "catchup-source", "catchup-days"}
	for _, attr := range attrs {
		val := extractAttr(line, attr)
		if val != "" {
			ch[attr] = val
		}
	}
	return ch
}

// ParseM3UFile parses an M3U file from disk
func ParseM3UFile(path string) ([]map[string]string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil { return nil, err }
	defer f.Close()
	return ParseM3U(f)
}
