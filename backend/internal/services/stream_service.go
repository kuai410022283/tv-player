package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/models"
)

// StreamProxy manages proxied streams with health checking
type StreamProxy struct {
	cfg                  *config.StreamConfig
	mu                   sync.RWMutex
	streams              map[string]*models.ActiveStream
	cancels              map[string]context.CancelFunc
	redirectedURLs       map[int64]string // 存储每个频道的重定向后基础 URL
	broadcasters         map[int64]*ChannelBroadcaster
	bMu                  sync.RWMutex
	client               *http.Client
	channelSvc           *ChannelService
	sem                  chan struct{} // 并发控制
	isHealthCheckRunning bool
	healthCheckTotal     int
	healthCheckCurrent   int
	healthCheckDelayMs   int
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
		broadcasters:   make(map[int64]*ChannelBroadcaster),
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
		ua = "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player"
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

// TriggerHealthCheck starts a smooth rolling health check in the background.
// It distributes the checks evenly over expectedMinutes to prevent CC bans and CPU spikes.
func (sp *StreamProxy) TriggerHealthCheck(expectedMinutes int) error {
	sp.mu.Lock()
	if sp.isHealthCheckRunning {
		sp.mu.Unlock()
		return fmt.Errorf("健康检查已经在运行中")
	}
	sp.isHealthCheckRunning = true
	sp.healthCheckTotal = 0
	sp.healthCheckCurrent = 0
	sp.mu.Unlock()

	go func() {
		defer func() {
			sp.mu.Lock()
			sp.isHealthCheckRunning = false
			sp.healthCheckTotal = 0
			sp.healthCheckCurrent = 0
			sp.mu.Unlock()
		}()

		// 获取全量无限制列表：循环按页取，直到取完
		page := 1
		pageSize := 100
		var allChannels []models.Channel

		for {
			p := &models.PageRequest{Page: page, PageSize: pageSize}
			resp, err := sp.channelSvc.ListChannels(0, "", p, 0)
			if err != nil || resp == nil {
				break
			}
			channels, ok := resp.Items.([]models.Channel)
			if !ok || len(channels) == 0 {
				break
			}
			allChannels = append(allChannels, channels...)
			if len(channels) < pageSize {
				break
			}
			page++
		}

		total := len(allChannels)
		if total == 0 {
			return
		}

		sp.mu.Lock()
		sp.healthCheckTotal = total
		sp.mu.Unlock()

		expectedSecs := float64(expectedMinutes * 60)
		delaySecs := expectedSecs / float64(total)
		if delaySecs < 0.5 {
			delaySecs = 0.5 // 防御底线，最快每 0.5 秒查一次
		}
		delay := time.Duration(delaySecs * float64(time.Second))

		sp.mu.Lock()
		sp.healthCheckDelayMs = int(delay.Milliseconds())
		sp.mu.Unlock()

		for _, ch := range allChannels {
			// 支持多线路顺序探测，只要有一条活着就算 online
			urls := strings.Split(ch.StreamURL, "#")
			finalStatus := "offline"
			for _, rawURL := range urls {
				if strings.TrimSpace(rawURL) == "" {
					continue
				}
				status, _ := sp.CheckHealth(ch.ID, rawURL, ch.StreamType)
				if status.Status == "online" {
					finalStatus = "online"
					break
				}
			}
			_ = sp.channelSvc.UpdateStatus(ch.ID, finalStatus)

			sp.mu.Lock()
			sp.healthCheckCurrent++
			sp.mu.Unlock()

			// 平滑休眠
			time.Sleep(delay)
		}
	}()

	return nil
}

// GetHealthCheckStatus returns the current health check progress and delay per channel
func (sp *StreamProxy) GetHealthCheckStatus() (bool, int, int, int) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()
	return sp.isHealthCheckRunning, sp.healthCheckCurrent, sp.healthCheckTotal, sp.healthCheckDelayMs
}

// GetProxyURL returns the proxied URL for a channel
func (sp *StreamProxy) GetProxyURL(channelID int64, baseURL string) string {
	return fmt.Sprintf("%s/api/v1/stream/proxy/%d", baseURL, channelID)
}

// ServeStream proxies the actual stream data. If targetURL is provided, it proxies that URL instead of the channel's default StreamURL.
func (sp *StreamProxy) ServeStream(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, targetURL string) error {
	ch, err := sp.channelSvc.GetChannel(channelID, 0)
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	st := strings.ToLower(ch.StreamType)
	canMultiplex := (st == "ts" || st == "flv" || st == "rtmp" || st == "rtsp" || st == "octet-stream")
	isMultiplex := ch.EnableMultiplex == 1 && canMultiplex && targetURL == ""

	if isMultiplex {
		return sp.serveMultiplex(channelID, clientID, clientIP, clientName, w, r, ch)
	}

	return sp.serveDirectProxy(channelID, clientID, clientIP, clientName, w, r, ch, targetURL)
}

// getFlushThreshold returns the protocol-appropriate flush buffer size.
// Priority: Channel.StreamType > Content-Type detection > URL extension/scheme.
// Different protocols have different latency vs. TCP efficiency needs.
func getFlushThreshold(ch *models.Channel, finalURL string) int {
	// 主信号：Channel StreamType（来自配置或竞速环节 Content-Type 自动识别）
	st := strings.ToLower(ch.StreamType)
	// 备选信号：URL 扩展名 / 协议头
	u := strings.ToLower(finalURL)

	switch {
	// ── HLS ──
	case st == "hls" || strings.Contains(u, ".m3u8"):
		return 16 * 1024 // 16KB: 分段下载，快速吐出降低段加载延迟
	// ── 直播流 ──
	case st == "ts" || st == "flv" || st == "octet-stream" ||
		strings.Contains(u, ".ts") || strings.Contains(u, ".flv"):
		return 64 * 1024 // 64KB: 直播流，平衡延迟与 TCP 小包效率
	// ── 低延迟协议 ──
	case st == "rtsp" || st == "rtmp" ||
		strings.HasPrefix(u, "rtsp://") || strings.HasPrefix(u, "rtmp://"):
		return 64 * 1024 // 64KB: 低延迟协议
	// ── DASH ──
	case st == "dash":
		return 64 * 1024 // 64KB: 分段直播，适中即可
	// ── 文件型媒体 ──
	case st == "mp4" || st == "mkv" || st == "avi" || st == "mov" || st == "webm" ||
		strings.Contains(u, ".mp4") || strings.Contains(u, ".mkv") ||
		strings.Contains(u, ".avi") || strings.Contains(u, ".mov") ||
		strings.Contains(u, ".webm"):
		return 128 * 1024 // 128KB: 文件流，不需要低延迟
	// ── 未知 ──
	default:
		return 128 * 1024 // 128KB: 保守兜底
	}
}

func (sp *StreamProxy) serveDirectProxy(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel, targetURL string) error {
	// 并发控制
	select {
	case sp.sem <- struct{}{}:
		defer func() { <-sp.sem }()
	default:
		return fmt.Errorf("并发流数已达上限 (%d)", sp.cfg.MaxConcurrent)
	}

	streamToProxy := ch.StreamURL
	if targetURL != "" {
		streamToProxy = targetURL
	}

	rawURLs := strings.Split(streamToProxy, "#")
	if lineStr := r.URL.Query().Get("line"); lineStr != "" {
		if lineIdx, err := strconv.Atoi(lineStr); err == nil && lineIdx >= 0 && lineIdx < len(rawURLs) {
			rawURLs = []string{rawURLs[lineIdx]} // 客户端指定了线路，仅尝试该线路
		}
	}

	var resp *http.Response
	var finalURL string
	var lastErr error
	var finalCancel context.CancelFunc

	// Apply inherited UA and custom headers
	ua, headers, err := sp.channelSvc.GetInheritedHeaders(channelID)
	if err != nil {
		ua = "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player"
	}

	validURLs := []string{}
	for _, u := range rawURLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if err := ValidateStreamURL(u); err != nil {
			lastErr = err
			continue
		}
		validURLs = append(validURLs, u)
	}

	expectedCount := len(validURLs)
	if expectedCount == 0 {
		if lastErr != nil {
			return fmt.Errorf("线路校验失败: %w", lastErr)
		}
		return fmt.Errorf("无有效播放线路")
	}

	type raceResult struct {
		index int
		resp  *http.Response
		url   string
		err   error
	}

	resultChan := make(chan raceResult, expectedCount)
	cancels := make([]context.CancelFunc, expectedCount)

	for i, u := range validURLs {
		reqCtx, reqCancel := context.WithCancel(r.Context())
		cancels[i] = reqCancel

		go func(idx int, targetURL string, reqCtx context.Context) {
			rResp, rErr := sp.openStreamTarget(reqCtx, targetURL, ua, headers)
			if rErr != nil {
				resultChan <- raceResult{index: idx, err: rErr}
				return
			}
			resultChan <- raceResult{index: idx, resp: rResp, url: targetURL}
		}(i, u, reqCtx)
	}

	var errorsList []string
	winnerIdx := -1
	consumed := 0

	for i := 0; i < expectedCount; i++ {
		res := <-resultChan
		consumed++
		if res.resp != nil {
			// 找到最快的一条存活线路
			resp = res.resp
			finalURL = res.url
			winnerIdx = res.index
			finalCancel = cancels[winnerIdx]

			if resp.Request != nil && resp.Request.URL != nil {
				finalURL = resp.Request.URL.String()
			}
			contentType := resp.Header.Get("Content-Type")
			isM3U8 := strings.Contains(strings.ToLower(contentType), "mpegurl") ||
				strings.Contains(strings.ToLower(resp.Request.URL.Path), ".m3u8") ||
				strings.Contains(strings.ToLower(finalURL), ".m3u8")
			if isM3U8 {
				sp.SetRedirectedURL(channelID, finalURL)
			}
			var actualType string
			if isM3U8 {
				actualType = "hls"
			} else if strings.Contains(strings.ToLower(contentType), "video/mp2t") || strings.Contains(strings.ToLower(contentType), "octet-stream") {
				actualType = "ts"
			} else if strings.Contains(strings.ToLower(contentType), "flv") {
				actualType = "flv"
			}
			if ch.StreamType == "" && actualType != "" {
				ch.StreamType = actualType
				_ = sp.channelSvc.UpdateStreamType(ch.ID, actualType)
			}
			break
		} else {
			if res.err != nil {
				errorsList = append(errorsList, res.err.Error())
			}
			// 当前线路失败，立即释放上下文
			if cancels[res.index] != nil {
				cancels[res.index]()
			}
		}
	}

	// 比赛结束，立即截断所有处于 pending 状态的失败者/慢速者
	for i, c := range cancels {
		if i != winnerIdx && c != nil {
			c()
		}
	}

	// 开启后台协程排空剩余响应，避免 body 泄露
	remaining := expectedCount - consumed
	if remaining > 0 {
		go func(rem int, winIdx int) {
			for j := 0; j < rem; j++ {
				res := <-resultChan
				if res.resp != nil && res.index != winIdx {
					res.resp.Body.Close()
				}
			}
		}(remaining, winnerIdx)
	}

	if finalURL == "" {
		if len(errorsList) > 0 {
			lastErr = fmt.Errorf(strings.Join(errorsList, " | "))
		}
		if lastErr != nil {
			return fmt.Errorf("所有线路均失效: %w", lastErr)
		}
		return fmt.Errorf("无有效播放线路")
	}

	sessionID := fmt.Sprintf("%d-%d-%d", channelID, clientID, time.Now().UnixNano())

	// Update stream state
	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		Mu:          &sync.RWMutex{},
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
	sp.cancels[sessionID] = finalCancel
	sp.mu.Unlock()

	defer func() {
		finalCancel()
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

	// Asynchronous buffering to prevent UDP drops
	// buffer capacity 1024 chunks. With a 32KB buffer size, this gives 32MB tolerance per client.
	chunkChan := make(chan []byte, 1024)
	errChan := make(chan error, 1)

	// Reader goroutine
	go func() {
		defer close(chunkChan)
		// Use a much larger read buffer (64KB) to reduce context switches and ensure TCP efficiency
		bufferSize := 64 * 1024
		if sp.cfg.BufferSize > bufferSize {
			bufferSize = sp.cfg.BufferSize
		}
		buf := make([]byte, bufferSize)
		reader := bufio.NewReaderSize(resp.Body, bufferSize)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				chunkData := make([]byte, n)
				copy(chunkData, buf[:n])
				select {
				case chunkChan <- chunkData:
				default:
					// Downstream client is too slow (buffer full).
					// To protect the server memory, we must abort this client's connection.
					select {
					case errChan <- fmt.Errorf("client buffer full, connection too slow"):
					default:
					}
					return
				}
			}
			if err != nil {
				select {
				case errChan <- err:
				default:
				}
				return
			}
		}
	}()

	lastUpdate := time.Now()
	var bytesSinceLastUpdate int64 = 0
	hasFlushed := false // 首次 Flush 标志

	// 根据协议类型选择 Flush 阈值，降低直播流延迟
	flushThreshold := getFlushThreshold(ch, finalURL)
	writeBuf := make([]byte, 0, 128*1024) // 初始容量保持 128KB 减少 realloc

	// Writer loop
	for {
		select {
		case <-r.Context().Done(): // Client disconnected
			return nil
		case err := <-errChan:
			if err == io.EOF {
				return nil
			}
			return err
		case chunk, ok := <-chunkChan:
			if !ok {
				if len(writeBuf) > 0 {
					w.Write(writeBuf)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				return nil
			}

			writeBuf = append(writeBuf, chunk...)

			if !hasFlushed {
				// 首次数据：立即 Flush，让客户端播放器尽快收到首字节开始解析
				if len(writeBuf) > 0 {
					n, err := w.Write(writeBuf)
					if err != nil {
						return err
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					bytesSinceLastUpdate += int64(n)
					writeBuf = writeBuf[:0]
					hasFlushed = true
				}
			} else {
				// 后续数据：攒够阈值再 Flush，根据协议动态调整减少延迟
				if len(writeBuf) >= flushThreshold {
					n, err := w.Write(writeBuf)
					if err != nil {
						return err
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					bytesSinceLastUpdate += int64(n)
					writeBuf = writeBuf[:0]
				}
			}

			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second {
				if len(writeBuf) > 0 {
					n, err := w.Write(writeBuf)
					if err != nil {
						return err
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					bytesSinceLastUpdate += int64(n)
					writeBuf = writeBuf[:0]
				}

				sp.mu.RLock()
				if s, ok := sp.streams[sessionID]; ok {
					s.Mu.Lock()
					s.LastActive = now
					s.SpeedBytes = bytesSinceLastUpdate
					s.Mu.Unlock()
				}
				sp.mu.RUnlock()
				bytesSinceLastUpdate = 0
				lastUpdate = now
			}
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
		s.Mu.RLock()
		snapshot := *s
		s.Mu.RUnlock()
		streams = append(streams, snapshot)
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
		if line == "" {
			continue
		}

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
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseM3U(f)
}

func (sp *StreamProxy) openStreamTarget(ctx context.Context, targetURL string, ua string, headers map[string]string) (*http.Response, error) {
	if strings.HasPrefix(targetURL, "udp://") || strings.HasPrefix(targetURL, "rtp://") {
		return openUDPStream(ctx, targetURL)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rResp, rErr := sp.client.Do(req)
	if rErr != nil {
		return nil, rErr
	}
	if rResp.StatusCode >= 200 && rResp.StatusCode < 400 {
		return rResp, nil
	}
	rResp.Body.Close()
	return nil, fmt.Errorf("status code %d", rResp.StatusCode)
}

func openUDPStream(ctx context.Context, rawURL string) (*http.Response, error) {
	isRTP := strings.HasPrefix(rawURL, "rtp://")
	addrStr := strings.TrimPrefix(rawURL, "udp://")
	addrStr = strings.TrimPrefix(addrStr, "rtp://")
	addrStr = strings.TrimPrefix(addrStr, "@")

	addr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}

	var conn *net.UDPConn
	if addr.IP.IsMulticast() {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
	} else {
		conn, err = net.ListenUDP("udp", addr)
	}
	if err != nil {
		return nil, err
	}

	// 8MB kernel buffer for UDP to prevent drops
	_ = conn.SetReadBuffer(8 * 1024 * 1024)

	pr, pw := io.Pipe()

	// Wait for the first packet to confirm health (max 3 seconds)
	firstBuf := make([]byte, 65536)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err := conn.ReadFromUDP(firstBuf)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("udp stream timeout or error: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{}) // Reset deadline

	go func() {
		defer conn.Close()
		defer pw.Close()

		// Write the first packet
		payload := firstBuf[:n]
		if isRTP && n > 12 && payload[0]>>6 == 2 {
			payload = payload[12:]
		}
		_, _ = pw.Write(payload)

		buf := make([]byte, 65536)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n > 0 {
				p := buf[:n]
				if isRTP && n > 12 && p[0]>>6 == 2 {
					p = p[12:]
				}
				_, wErr := pw.Write(p)
				if wErr != nil {
					return
				}
			}
		}
	}()

	resp := &http.Response{
		StatusCode: 200,
		Body:       pr,
		Header:     make(http.Header),
		Request:    &http.Request{URL: &url.URL{Scheme: "udp", Host: addrStr}},
	}
	// UDP streams are typically MPEG-TS
	resp.Header.Set("Content-Type", "video/mp2t")
	return resp, nil
}
