package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services/multicast"
	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtp"
)

var streamBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 128*1024)
		return &buf
	},
}

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
				TLSHandshakeTimeout:   10 * time.Second, // TLS 握手超时（部分 HTTPS 源握手慢）
				ResponseHeaderTimeout: 30 * time.Second, // 等待上游响应头的最长时间
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
			// 允许跨协议重定向（如 HTTPS→HTTP），限制最大跳数避免循环
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				// 保持 Referer 随重定向传递
				if len(via) > 0 {
					req.Header.Set("Referer", via[len(via)-1].URL.String())
				}
				return nil
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
// Priority: Channel.StreamType > Original source URL (ch.StreamURL) > Content-Type detection > finalURL.
// Different protocols have different latency vs. TCP efficiency needs.
func getFlushThreshold(ch *models.Channel, finalURL string) int {
	// 主信号：Channel StreamType（来自配置或竞速环节 Content-Type 自动识别）
	st := strings.ToLower(ch.StreamType)
	// 核心信号：原始源地址（代理 URL 中不含 /rtp/ 等模式，必须用原始源判断）
	src := strings.ToLower(ch.StreamURL)
	// 备选信号：上游实际连接地址（可能经过重定向丢失模式）
	u := strings.ToLower(finalURL)
	// 合并判断：原始源 + 实际连接地址
	allURL := src + " " + u

	switch {
	// ── HLS ──
	case st == "hls" || strings.Contains(allURL, ".m3u8"):
		return 16 * 1024 // 16KB: 分段下载，快速吐出降低段加载延迟
	// ── 直播流 ──
	case st == "ts" || st == "flv" || st == "octet-stream" ||
		hasPathSuffix(src, ".ts") || hasPathSuffix(src, ".flv") ||
		hasPathSuffix(u, ".ts") || hasPathSuffix(u, ".flv"):
		return 512 * 1024 // 512KB: 降低 HTTP Chunk 开销，解决 4K 播放卡顿问题
	// ── 低延迟协议 ──
	case st == "rtsp" || st == "rtmp" ||
		strings.HasPrefix(src, "rtsp://") || strings.HasPrefix(src, "rtmp://") ||
		strings.HasPrefix(u, "rtsp://") || strings.HasPrefix(u, "rtmp://"):
		return 128 * 1024 // 128KB: 低延迟协议
	// ── RTP-over-HTTP / 组播网关（必须用原始源判断，代理 URL 不含 /rtp/）──
	case strings.Contains(allURL, "/rtp/") || strings.Contains(allURL, "/udp/") ||
		strings.Contains(allURL, "multicast") || strings.Contains(allURL, "igmp"):
		return 512 * 1024 // 512KB: RTP/UDP 实时流，大幅增加块大小以支撑 4K 高码率
	// ── DASH ──
	case st == "dash":
		return 64 * 1024 // 64KB: 分段直播，适中即可
	// ── 文件型媒体 ──
	case st == "mp4" || st == "mkv" || st == "avi" || st == "mov" || st == "webm" ||
		strings.Contains(allURL, ".mp4") || strings.Contains(allURL, ".mkv") ||
		strings.Contains(allURL, ".avi") || strings.Contains(allURL, ".mov") ||
		strings.Contains(allURL, ".webm"):
		return 128 * 1024 // 128KB: 文件流，不需要低延迟
	// ── 未知 ──
	default:
		return 128 * 1024 // 128KB: 保守兜底
	}
}

// hasPathSuffix safely parses the URL and checks if its path ends with the given suffix.
func hasPathSuffix(rawURL, suffix string) bool {
	if u, err := url.Parse(rawURL); err == nil {
		return strings.HasSuffix(strings.ToLower(u.Path), suffix)
	}
	return false
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

	firstURL := validURLs[0]
	if strings.HasPrefix(firstURL, "udp://") || strings.HasPrefix(firstURL, "rtp://") {
		return sp.serveMulticastProxy(channelID, clientID, clientIP, clientName, w, r, ch, firstURL)
	}
	if strings.HasPrefix(firstURL, "rtsp://") {
		return sp.serveRtspProxy(channelID, clientID, clientIP, clientName, w, r, ch, firstURL)
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

			slog.Info("stream proxy upstream connected", "channel_id", channelID, "winner", winnerIdx, "url", finalURL, "status", resp.StatusCode, "content_type", resp.Header.Get("Content-Type"))
			contentType := resp.Header.Get("Content-Type")
			isM3U8 := strings.Contains(strings.ToLower(contentType), "mpegurl") ||
				strings.Contains(strings.ToLower(resp.Request.URL.Path), ".m3u8") ||
				strings.Contains(strings.ToLower(finalURL), ".m3u8")
			
			// 只有在主请求（非子路径分片请求）时，才更新该频道的 RedirectedURL
			if targetURL == "" {
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
				// 类型完全未知，直接写入检测结果
				ch.StreamType = actualType
				_ = sp.channelSvc.UpdateStreamType(ch.ID, actualType)
			} else if ch.StreamType == "ts" && actualType == "hls" {
				// URL 无后缀时静态检测兜底为 ts，但 Content-Type 确认是 HLS
				// 首次播放时自动修正，后续播放直接使用 16KB 低延迟模式
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
			lastErr = fmt.Errorf("%s", strings.Join(errorsList, " | "))
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

	// 根据协议类型选择 Flush 阈值
	flushThreshold := getFlushThreshold(ch, finalURL)
	isLowLatency := flushThreshold <= 64*1024

	flusher, canFlush := w.(http.Flusher)

	if isLowLatency && canFlush {
		// ═══════════════════════════════════════════════════════
		// 低延迟 Pipe 模式：累积写入 + 2ms 超时 Flush，避免小包刷屏
		// ═══════════════════════════════════════════════════════
		slog.Info("stream proxy pipe mode", "channel_id", channelID, "session", sessionID, "url", finalURL, "threshold", flushThreshold)

		reader := bufio.NewReaderSize(resp.Body, 64*1024)
		readBuf := make([]byte, 64*1024)
		writeBuf := make([]byte, 0, 128*1024)
		lastUpdate := time.Now()
		lastFlush := time.Now()
		var bytesSinceLastUpdate int64 = 0
		var bytesRead int64 = 0

		for {
			n, err := reader.Read(readBuf)
			if n > 0 {
				bytesRead += int64(n)
				writeBuf = append(writeBuf, readBuf[:n]...)

				// 累积到 16KB 或 2ms 超时再 Flush，平衡延迟与 TCP 效率
				shouldFlush := len(writeBuf) >= 16*1024 || time.Since(lastFlush) >= 2*time.Millisecond
				if shouldFlush {
					if _, wErr := w.Write(writeBuf); wErr != nil {
						slog.Info("stream proxy client disconnected (pipe)", "session", sessionID, "bytes", bytesRead)
						return nil
					}
					flusher.Flush()
					bytesSinceLastUpdate += int64(len(writeBuf))
					writeBuf = writeBuf[:0]
					lastFlush = time.Now()
				}
			}
			if err != nil {
				// 流结束：刷出残余数据
				if len(writeBuf) > 0 {
					w.Write(writeBuf)
					flusher.Flush()
				}
				if err == io.EOF {
					slog.Info("stream proxy upstream EOF (pipe)", "session", sessionID, "bytes", bytesRead)
					return nil
				}
				slog.Error("stream proxy upstream error (pipe)", "session", sessionID, "error", err, "bytes", bytesRead)
				return err
			}

			// 客户端断开检测
			select {
			case <-r.Context().Done():
				slog.Info("stream proxy client disconnected (pipe)", "session", sessionID, "bytes", bytesRead)
				return nil
			default:
			}

			// 每秒更新流状态统计
			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second {
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
	} else {
		// ═══════════════════════════════════════════════════════
		// 缓冲模式：适合 HLS/DASH/文件等非实时流
		// ═══════════════════════════════════════════════════════
		slog.Info("stream proxy buffered mode", "channel_id", channelID, "session", sessionID, "url", finalURL, "threshold", flushThreshold)

		bufferSize := 64 * 1024
		if sp.cfg.BufferSize > bufferSize {
			bufferSize = sp.cfg.BufferSize
		}
		if bufferSize > 128*1024 {
			bufferSize = 128 * 1024
		}
		
		bufPtr := streamBufferPool.Get().(*[]byte)
		buf := (*bufPtr)[:bufferSize]
		defer streamBufferPool.Put(bufPtr)
		reader := bufio.NewReaderSize(resp.Body, bufferSize)

		writeBufPtr := streamBufferPool.Get().(*[]byte)
		writeBuf := (*writeBufPtr)[:0]
		defer streamBufferPool.Put(writeBufPtr)

		lastUpdate := time.Now()
		var bytesSinceLastUpdate int64 = 0
		hasFlushed := false

		for {
			select {
			case <-r.Context().Done():
				return nil
			default:
			}

			n, err := reader.Read(buf)
			if n > 0 {
				writeBuf = append(writeBuf, buf[:n]...)

				if !hasFlushed {
					if len(writeBuf) > 0 {
						wn, wErr := w.Write(writeBuf)
						if wErr != nil {
							return nil
						}
						if canFlush {
							flusher.Flush()
						}
						bytesSinceLastUpdate += int64(wn)
						writeBuf = writeBuf[:0]
						hasFlushed = true
					}
				} else if len(writeBuf) >= flushThreshold {
					wn, wErr := w.Write(writeBuf)
					if wErr != nil {
						return nil
					}
					if canFlush {
						flusher.Flush()
					}
					bytesSinceLastUpdate += int64(wn)
					writeBuf = writeBuf[:0]
				}
			}

			if err != nil {
				if len(writeBuf) > 0 {
					w.Write(writeBuf)
					if canFlush {
						flusher.Flush()
					}
				}
				if err == io.EOF {
					return nil
				}
				return err
			}

			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second {
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

	// 检测是否为咪咕/华数等需定制 UA 的源（通过 URL 中的 appCode 等参数判断）
	isMigU := strings.Contains(strings.ToLower(targetURL), "appcode=miguvideo") ||
		strings.Contains(strings.ToLower(targetURL), "bean=mgspad")

	// 准备多个 User-Agent 候补列表
	var userAgents []string
	if isMigU {
		// 咪咕源：优先使用 Android MiguVideo App 风格 UA
		userAgents = []string{
			"MiguVideo/9.6.0 (Linux; Android 14; SM-S928B) tv.danmaku.bili",
			"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.230 Mobile Safari/537.36",
			"ExoPlayerLib/2.19.1 (Linux; Android 14) ExoPlayerLib/2.19.1",
		}
	} else {
		userAgents = []string{ua}
		if ua != "" {
			userAgents = append(userAgents,
				"ExoPlayerLib/2.19.1 (Linux; Android 14) ExoPlayerLib/2.19.1",
				"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.230 Mobile Safari/537.36",
			)
		} else {
			userAgents = []string{
				"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.230 Mobile Safari/537.36",
				"ExoPlayerLib/2.19.1 (Linux; Android 14) ExoPlayerLib/2.19.1",
			}
		}
	}

	// 最多尝试所有 UA，遇 403/5xx 时重试不同 UA
	for attempt, currentUA := range userAgents {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", targetURL, nil)
		if err != nil {
			if attempt < len(userAgents)-1 {
				continue
			}
			return nil, err
		}
		req.Header.Set("User-Agent", currentUA)

		// 移除强制设置自身为 Referer 的逻辑，避免触发 PHP 防盗链
		// 自定义 Referer 会由下面的 headers 注入逻辑处理
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		rResp, rErr := sp.client.Do(req)
		if rErr != nil {
			if attempt < len(userAgents)-1 {
				continue
			}
			return nil, rErr
		}
		if rResp.StatusCode >= 200 && rResp.StatusCode < 400 {
			return rResp, nil
		}
		rResp.Body.Close()

		// 可重试状态码：403 (Forbidden，换 UA 可能绕过)、5xx (Server Error，临时故障)
		// 不可重试：400、401、404 等（换 UA 无效）
		if rResp.StatusCode != 403 && rResp.StatusCode < 500 {
			return nil, fmt.Errorf("status code %d", rResp.StatusCode)
		}
		if attempt == len(userAgents)-1 {
			return nil, fmt.Errorf("status code %d", rResp.StatusCode)
		}
	}

	return nil, fmt.Errorf("all attempts failed")
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

func (sp *StreamProxy) serveMulticastProxy(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel, targetURL string) error {
	sessionID := fmt.Sprintf("multicast-%d-%s", channelID, uuid.New().String())

	slog.Info("starting multicast proxy", "channel_id", channelID, "url", targetURL)

	reader, err := multicast.NewMulticastReader(r.Context(), targetURL)
	if err != nil {
		return fmt.Errorf("failed to open multicast reader: %w", err)
	}
	defer reader.Close()

	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)

	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		Mu:          &sync.RWMutex{},
		SessionID:   sessionID,
		ChannelID:   channelID,
		ChannelName: ch.Name,
		ClientID:    clientID,
		ClientName:  clientName,
		ClientIP:    clientIP,
		URL:         targetURL,
		Status:      "playing_multicast",
		StartedAt:   time.Now(),
		LastActive:  time.Now(),
	}
	sp.cancels[sessionID] = func() {} // dummy cancel
	sp.mu.Unlock()

	defer func() {
		sp.mu.Lock()
		delete(sp.streams, sessionID)
		delete(sp.cancels, sessionID)
		sp.mu.Unlock()
	}()

	flusher, canFlush := w.(http.Flusher)
	bufPtr := streamBufferPool.Get().(*[]byte)
	buf := (*bufPtr)[:128*1024]
	defer streamBufferPool.Put(bufPtr)
	var bytesRead int64

	lastUpdate := time.Now()
	var bytesSinceLastUpdate int64

	for {
		select {
		case <-r.Context().Done():
			slog.Info("multicast proxy client disconnected", "session", sessionID)
			return nil
		default:
		}

		n, err := reader.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return nil
			}
			if canFlush {
				flusher.Flush()
			}
			bytesRead += int64(n)
			bytesSinceLastUpdate += int64(n)
		}
		if err != nil {
			slog.Error("multicast proxy read error", "session", sessionID, "error", err)
			return err
		}

		now := time.Now()
		if now.Sub(lastUpdate) >= time.Second {
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

func (sp *StreamProxy) serveRtspProxy(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel, targetURL string) error {
	sessionID := fmt.Sprintf("rtsp-%d-%s", channelID, uuid.New().String())

	slog.Info("starting rtsp proxy", "channel_id", channelID, "url", targetURL)

	c := &gortsplib.Client{}
	
	// Watch for client disconnect to prevent goroutine leak
	go func() {
		<-r.Context().Done()
		c.Close()
	}()
	
	u, err := base.ParseURL(targetURL)
	if err != nil {
		return err
	}
	
	err = c.Start(u.Scheme, u.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return err
	}

	err = c.SetupAll(desc.BaseURL, desc.Medias)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(http.StatusOK)

	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		Mu:          &sync.RWMutex{},
		SessionID:   sessionID,
		ChannelID:   channelID,
		ChannelName: ch.Name,
		ClientID:    clientID,
		ClientName:  clientName,
		ClientIP:    clientIP,
		URL:         targetURL,
		Status:      "playing_rtsp",
		StartedAt:   time.Now(),
		LastActive:  time.Now(),
	}
	sp.cancels[sessionID] = func() { c.Close() }
	sp.mu.Unlock()

	defer func() {
		sp.mu.Lock()
		delete(sp.streams, sessionID)
		delete(sp.cancels, sessionID)
		sp.mu.Unlock()
	}()

	flusher, canFlush := w.(http.Flusher)
	
	lastUpdate := time.Now()
	var bytesSinceLastUpdate int64

	c.OnPacketRTPAny(func(medi *description.Media, forma format.Format, pkt *rtp.Packet) {
		// Just strip RTP header and write payload assuming it's TS over RTSP or playable raw payload
		n, wErr := w.Write(pkt.Payload)
		if wErr != nil {
			c.Close() // Force stop on client disconnect
			return
		}
		
		bytesSinceLastUpdate += int64(n)
		
		now := time.Now()
		if now.Sub(lastUpdate) >= time.Millisecond * 100 {
			if canFlush {
				flusher.Flush()
			}
			
			if now.Sub(lastUpdate) >= time.Second {
				sp.mu.RLock()
				if s, ok := sp.streams[sessionID]; ok {
					s.Mu.Lock()
					s.LastActive = now
					s.SpeedBytes = bytesSinceLastUpdate
					s.Mu.Unlock()
				}
				sp.mu.RUnlock()
				bytesSinceLastUpdate = 0
			}
			lastUpdate = now
		}
	})

	_, err = c.Play(nil)
	if err != nil {
		return err
	}

	return c.Wait()
}

