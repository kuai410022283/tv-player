package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/google/uuid"
	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/models"
	"github.com/mediaplayer/backend/internal/services/multicast"
	"github.com/pion/rtp"
	"golang.org/x/net/proxy"
)

var streamBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 128*1024)
		return &buf
	},
}

// proxyErrorCount 统计代理连接失败次数（运行时可观测指标）
var proxyErrorCount int64

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
	_ = os.MkdirAll(cfg.CacheDir, 0755)
	return sp
}

// getProxyClient 根据频道的代理配置创建带代理的 HTTP 客户端
// 如果频道没有代理配置，返回默认客户端
func (sp *StreamProxy) getProxyClient(ch *models.Channel) *http.Client {
	// 获取继承的代理配置
	proxyType, proxyURL := sp.getInheritedProxy(ch)

	if proxyType == "" || proxyURL == "" {
		return sp.client
	}

	proxyParsed, err := url.Parse(proxyURL)
	if err != nil {
		atomic.AddInt64(&proxyErrorCount, 1)
		slog.Error("SOCKS5代理地址格式错误，已回退直连", "proxy_url", proxyURL, "error", err, "total_errors", atomic.LoadInt64(&proxyErrorCount))
		return sp.client
	}

	switch proxyType {
	case "socks5":
		// 创建 SOCKS5 代理拨号器
		var auth *proxy.Auth
		if proxyParsed.User != nil {
			password, _ := proxyParsed.User.Password()
			auth = &proxy.Auth{
				User:     proxyParsed.User.Username(),
				Password: password,
			}
		}
		dialer, err := proxy.SOCKS5("tcp", proxyParsed.Host, auth, proxy.Direct)
		if err != nil {
			atomic.AddInt64(&proxyErrorCount, 1)
			slog.Error("SOCKS5代理服务器连接失败，已回退直连", "proxy_url", proxyURL, "error", err, "total_errors", atomic.LoadInt64(&proxyErrorCount))
			return sp.client
		}

		return &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       90 * time.Second,
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
			},
			CheckRedirect: sp.client.CheckRedirect,
		}
	default:
		slog.Warn("不支持的代理类型", "proxy_type", proxyType)
		return sp.client
	}
}

// getInheritedProxy 获取频道继承的代理配置（Channel > ChannelGroup > M3USource）
// proxyType="none" 表示显式禁用代理（覆盖继承），空字符串表示继续向上继承
func (sp *StreamProxy) getInheritedProxy(ch *models.Channel) (string, string) {
	// 1. 频道级别
	if ch.ProxyType == "none" {
		return "", "" // 显式禁用代理，不继承
	}
	if ch.ProxyType != "" && ch.ProxyURL != "" {
		return ch.ProxyType, ch.ProxyURL
	}

	// 2. 分组级别
	if ch.GroupID > 0 {
		group, err := sp.channelSvc.GetGroup(ch.GroupID)
		if err == nil {
			if group.ProxyType == "none" {
				return "", "" // 分组显式禁用，不继承源
			}
			if group.ProxyType != "" && group.ProxyURL != "" {
				return group.ProxyType, group.ProxyURL
			}
		}
	}

	// 3. 源级别（通过 m3u_source_id）
	if ch.M3USourceID > 0 {
		var proxyType, proxyURL string
		err := sp.channelSvc.db.QueryRow(
			"SELECT proxy_type, proxy_url FROM m3u_sources WHERE id = ?", ch.M3USourceID,
		).Scan(&proxyType, &proxyURL)
		if err == nil && proxyType != "" && proxyURL != "" {
			return proxyType, proxyURL
		}
	}

	return "", ""
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

// resolveStrmUrl intercepts .strm URLs, fetches them, and extracts the first valid http(s) link.
// It supports up to 5 levels of redirection (nested .strm files).
func (sp *StreamProxy) resolveStrmUrl(ctx context.Context, initialURL string, ua string, headers map[string]string, ch *models.Channel) (string, error) {
	currentURL := initialURL
	for i := 0; i < 5; i++ {
		if !strings.HasSuffix(strings.ToLower(currentURL), ".strm") {
			return currentURL, nil
		}

		var scanner *bufio.Scanner
		var bodyToClose io.ReadCloser

		if isLocalPath(currentURL) {
			f, err := os.Open(filepath.Clean(currentURL))
			if err != nil {
				return currentURL, fmt.Errorf("local strm open failed: %v", err)
			}
			scanner = bufio.NewScanner(f)
			bodyToClose = f
		} else {
			req, err := http.NewRequestWithContext(ctx, "GET", currentURL, nil)
			if err != nil {
				return currentURL, err
			}
			if ua != "" {
				req.Header.Set("User-Agent", ua)
			} else {
				req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0")
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			// 使用带代理的客户端
			proxyClient := sp.getProxyClient(ch)
			resp, err := proxyClient.Do(req)
			if err != nil {
				return currentURL, err
			}

			if resp.StatusCode != 200 {
				resp.Body.Close()
				return currentURL, fmt.Errorf("strm returned status %d", resp.StatusCode)
			}
			scanner = bufio.NewScanner(resp.Body)
			bodyToClose = resp.Body
		}

		// Read up to 10KB to prevent memory exhaustion from malicious large files
		var extracted string
		bytesRead := 0
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			bytesRead += len(line)
			if bytesRead > 10240 { // limit to ~10KB
				break
			}
			if strings.HasPrefix(strings.ToLower(line), "http://") || strings.HasPrefix(strings.ToLower(line), "https://") {
				extracted = line
				break
			}
		}
		if bodyToClose != nil {
			bodyToClose.Close()
		}

		if extracted != "" {
			currentURL = extracted
		} else {
			return currentURL, nil // Return the original strm if no valid link is found
		}
	}
	return currentURL, nil
}

// CheckHealth verifies a stream URL is reachable and returns stream info
// CheckHealth verifies a stream URL is reachable and returns stream info
func (sp *StreamProxy) CheckHealth(channelID int64, lineIdx int, rawURL, streamType string) (*models.StreamStatus, error) {
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

	// 获取自定义的 User-Agent 和 Headers
	ua, headers, _ := sp.channelSvc.GetInheritedHeaders(channelID)
	if ua == "" {
		ua = "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0"
	}

	// 获取频道信息（用于代理配置）
	ch, _ := sp.channelSvc.GetChannel(channelID, 0)

	// 无论本地还是网络，探测前统一穿透可能的 .strm 壳
	resolvedURL, err := sp.resolveStrmUrl(context.Background(), url, ua, headers, ch)
	if err == nil && resolvedURL != "" {
		url = resolvedURL
		status.URL = url // 更新 status 里的 URL 为真实流地址
	}

	// 校验 URL
	if err := ValidateStreamURL(url); err != nil {
		status.Status = "error"
		status.ErrorMsg = "URL 不安全: " + err.Error()
		return status, err
	}

	// 健康检查用独立短超时 client，禁用 KeepAlive 避免关闭未读完的响应体导致闲置连接接收到乱码
	// 如果频道配置了代理，健康检查也需要走代理
	var healthClient *http.Client
	proxyClient := sp.getProxyClient(ch)
	if proxyClient != sp.client {
		// 有代理配置：复制一份并设置健康检查专用参数
		transport := proxyClient.Transport.(*http.Transport).Clone()
		transport.DisableKeepAlives = true
		healthClient = &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		}
	} else {
		// 无代理：使用独立短超时 client
		healthClient = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
		}
	}

	if streamType == "" {
		lowerURL := strings.ToLower(url)
		if strings.HasPrefix(lowerURL, "rtmp://") {
			streamType = "rtmp"
			_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, streamType)
		} else if strings.HasPrefix(lowerURL, "rtsp://") {
			streamType = "rtsp"
			_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, streamType)
		} else if strings.HasPrefix(lowerURL, "udp://") || strings.HasPrefix(lowerURL, "rtp://") {
			streamType = "udp"
			_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, streamType)
		}
	}

	// 本地文件健康检查：用 os.Stat 替代 HTTP GET
	if isLocalPath(url) {
		cleanPath := filepath.Clean(url)
		info, err := os.Stat(cleanPath)
		if err != nil {
			status.Status = "error"
			status.ErrorMsg = "本地文件不存在: " + err.Error()
			return status, nil
		}
		if info.IsDir() {
			status.Status = "error"
			status.ErrorMsg = "路径是目录而非文件"
			return status, nil
		}
		status.Status = "online"
		if streamType == "" {
			ext := strings.ToLower(filepath.Ext(cleanPath))
			switch ext {
			case ".m3u8":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "hls")
			case ".mp4":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "mp4")
			case ".mkv":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "mkv")
			case ".avi":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "avi")
			case ".flv":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "flv")
			case ".ts":
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, "ts")
			}
		}
		return status, nil
	}

	switch streamType {
	case "hls", "mp4", "dash", "flv", "ts", "":
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

			// Detect stream type
			finalURL := resp.Request.URL.String()
			contentType := resp.Header.Get("Content-Type")
			isM3U8 := strings.Contains(strings.ToLower(contentType), "mpegurl") ||
				strings.Contains(strings.ToLower(resp.Request.URL.Path), ".m3u8") ||
				strings.Contains(strings.ToLower(finalURL), ".m3u8")

			var actualType string
			if isM3U8 {
				actualType = "hls"
			} else if strings.Contains(strings.ToLower(contentType), "video/mp2t") || strings.Contains(strings.ToLower(contentType), "octet-stream") {
				actualType = "ts"
			} else if strings.Contains(strings.ToLower(contentType), "flv") {
				actualType = "flv"
			}

			if streamType == "" && actualType != "" {
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, actualType)
			} else if streamType == "ts" && actualType == "hls" {
				_ = sp.channelSvc.UpdateStreamType(channelID, lineIdx, actualType)
			}
		} else {
			status.Status = "offline"
			status.ErrorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	case "rtmp", "rtsp", "udp":
		// For RTMP/RTSP/UDP, we just mark as online (simplified; real impl would use ffmpeg probe)
		status.Status = "online"
	default:
		status.Status = "unknown"
	}
	return status, nil
}

// TriggerHealthCheck starts a smooth rolling health check in the background.
// It distributes the checks evenly over expectedMinutes to prevent CC bans and CPU spikes.
func (sp *StreamProxy) TriggerHealthCheck(expectedMinutes int, ids []int64) error {
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

		var allChannels []models.Channel

		if len(ids) > 0 {
			// Fetch specific channels
			for _, id := range ids {
				ch, err := sp.channelSvc.GetChannel(id, 0)
				if err == nil && ch != nil {
					allChannels = append(allChannels, *ch)
				}
			}
		} else {
			// 获取全量无限制列表：循环按页取，直到取完
			page := 1
			pageSize := 100

			for {
				p := &models.PageRequest{Page: page, PageSize: pageSize}
				resp, err := sp.channelSvc.ListChannels(0, "", "", nil, p, 0)
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
			types := strings.Split(ch.StreamType, "#")
			finalStatus := "offline"
			for i, rawURL := range urls {
				if strings.TrimSpace(rawURL) == "" {
					continue
				}
				lineType := ""
				if i < len(types) {
					lineType = types[i]
				}
				status, _ := sp.CheckHealth(ch.ID, i, rawURL, lineType)
				if status.Status == "online" {
					finalStatus = "online"
				}

				// 防止多线路并发过快被封，线路间增加微小间隔
				if len(urls) > 1 && i < len(urls)-1 {
					time.Sleep(500 * time.Millisecond)
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

// IsStreamTypeMultiplexable checks if the stream type supports multiplexing.
// For multi-line channels (e.g., ts#ts#ts), all lines must support multiplexing.
func IsStreamTypeMultiplexable(streamType string) bool {
	if streamType == "" {
		return false
	}
	types := strings.Split(strings.ToLower(streamType), "#")
	for _, st := range types {
		if st != "ts" && st != "flv" && st != "rtmp" && st != "rtsp" && st != "octet-stream" {
			return false
		}
	}
	return true
}

// ServeStream proxies the actual stream data. If targetURL is provided, it proxies that URL instead of the channel's default StreamURL.
func (sp *StreamProxy) ServeStream(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, targetURL string) error {
	ch, err := sp.channelSvc.GetChannel(channelID, 0)
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	canMultiplex := IsStreamTypeMultiplexable(ch.StreamType)
	isMultiplex := ch.EnableMultiplex == 1 && canMultiplex && targetURL == ""

	if isMultiplex {
		return sp.serveMultiplex(channelID, clientID, clientIP, clientName, w, r, ch)
	}

	return sp.serveDirectProxy(channelID, clientID, clientIP, clientName, w, r, ch, targetURL)
}

// ServeLocalProxy bypasses database lookups and directly proxies a targetURL. Used by the Android mobile wrapper.
func (sp *StreamProxy) ServeLocalProxy(w http.ResponseWriter, r *http.Request, targetURL string) error {
	ch := &models.Channel{
		ID:        0,
		Name:      "LocalProxy",
		StreamURL: targetURL,
	}
	return sp.serveDirectProxy(0, 0, "127.0.0.1", "AndroidTV", w, r, ch, targetURL)
}

// getFlushThreshold returns the protocol-appropriate flush buffer size.
// Priority: Channel.StreamType > Original source URL (ch.StreamURL) > Content-Type detection > finalURL.
// Different protocols have different latency vs. TCP efficiency needs.
func getFlushThreshold(streamType string, originalLineURL string, finalURL string) int {
	// 主信号：Channel StreamType（来自配置或竞速环节 Content-Type 自动识别）
	st := strings.ToLower(streamType)
	// 核心信号：原始源地址（代理 URL 中不含 /rtp/ 等模式，必须用原始源判断）
	src := strings.ToLower(originalLineURL)
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
	actualLineIndices := make([]int, len(rawURLs))
	for i := range rawURLs {
		actualLineIndices[i] = i
	}
	if lineStr := r.URL.Query().Get("line"); lineStr != "" {
		if lineIdx, err := strconv.Atoi(lineStr); err == nil && lineIdx >= 0 && lineIdx < len(rawURLs) {
			rawURLs = []string{rawURLs[lineIdx]} // 客户端指定了线路，仅尝试该线路
			actualLineIndices = []int{lineIdx}
		}
	}

	var resp *http.Response
	var finalURL string
	var lastErr error
	var finalCancel context.CancelFunc

	// Apply inherited UA and custom headers
	ua, headers, err := sp.channelSvc.GetInheritedHeaders(channelID)
	if err != nil {
		ua = "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0"
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

	// 提前进行 strm 穿透清洗，无论 HTTP 还是本地路径
	for i, u := range validURLs {
		resolvedURL, err := sp.resolveStrmUrl(r.Context(), u, ua, headers, ch)
		if err == nil && resolvedURL != "" {
			validURLs[i] = resolvedURL
		}
	}

	firstURL := validURLs[0]
	if strings.HasPrefix(firstURL, "udp://") || strings.HasPrefix(firstURL, "rtp://") {
		return sp.serveMulticastProxy(channelID, clientID, clientIP, clientName, w, r, ch, firstURL)
	}
	if strings.HasPrefix(firstURL, "rtsp://") {
		return sp.serveRtspProxy(channelID, clientID, clientIP, clientName, w, r, ch, firstURL)
	}
	if isLocalPath(firstURL) {
		return sp.serveLocalFileProxy(channelID, clientID, clientIP, clientName, w, r, ch, firstURL)
	}

	type raceResult struct {
		index int
		resp  *http.Response
		url   string
		err   error
	}

	resultChan := make(chan raceResult, expectedCount)
	cancels := make([]context.CancelFunc, expectedCount)
	var winnerActualType string

	for i, u := range validURLs {
		reqCtx, reqCancel := context.WithCancel(r.Context())
		cancels[i] = reqCancel

		go func(idx int, targetURL string, reqCtx context.Context) {
			rResp, rErr := sp.openStreamTarget(reqCtx, targetURL, ua, headers, ch)
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
			winnerActualType = actualType
			types := strings.Split(ch.StreamType, "#")
			currentType := ""
			realLineIdx := actualLineIndices[winnerIdx]
			if realLineIdx < len(types) {
				currentType = types[realLineIdx]
			}

			if currentType == "" && actualType != "" {
				// 类型完全未知，直接写入检测结果
				_ = sp.channelSvc.UpdateStreamType(ch.ID, realLineIdx, actualType)
			} else if currentType == "ts" && actualType == "hls" {
				// URL 无后缀时静态检测兜底为 ts，但 Content-Type 确认是 HLS
				// 首次播放时自动修正，后续播放直接使用 16KB 低延迟模式
				_ = sp.channelSvc.UpdateStreamType(ch.ID, realLineIdx, actualType)
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
	winnerLineType := ""
	types := strings.Split(ch.StreamType, "#")
	if actualLineIndices[winnerIdx] < len(types) {
		winnerLineType = types[actualLineIndices[winnerIdx]]
	}
	if winnerLineType == "" && winnerActualType != "" {
		winnerLineType = winnerActualType
	}
	flushThreshold := getFlushThreshold(winnerLineType, validURLs[winnerIdx], finalURL)
	baseThreshold := flushThreshold
	flusher, canFlush := w.(http.Flusher)

	// 1. 设置动态上限与超时底线
	var maxThreshold int
	var maxLatency time.Duration

	if baseThreshold <= 16*1024 { // HLS 等极端实时
		maxThreshold = 64 * 1024
		maxLatency = 2 * time.Millisecond
	} else if baseThreshold <= 64*1024 { // 直播 TS
		maxThreshold = 256 * 1024
		maxLatency = 100 * time.Millisecond
	} else if baseThreshold <= 128*1024 { // RTSP/RTMP/未知
		maxThreshold = 2048 * 1024
		maxLatency = 500 * time.Millisecond
	} else { // RTP 大包
		maxThreshold = 2048 * 1024
		maxLatency = 200 * time.Millisecond
	}

	slog.Info("stream proxy unified dynamic mode", "channel_id", channelID, "session", sessionID, "url", finalURL, "baseThreshold", baseThreshold, "maxLatency", maxLatency)

	// 2. 初始化缓冲区
	reader := bufio.NewReaderSize(resp.Body, 128*1024)
	readBuf := make([]byte, 128*1024)
	writeBuf := make([]byte, 0, maxThreshold+128*1024) // 预留余量

	lastUpdate := time.Now()
	lastFlush := time.Now()
	var bytesSinceLastUpdate int64 = 0
	var bytesRead int64 = 0

	currentThreshold := baseThreshold
	hasFlushed := false

	// 3. 统一智能读写循环
	for {
		n, err := reader.Read(readBuf)
		if n > 0 {
			bytesRead += int64(n)
			writeBuf = append(writeBuf, readBuf[:n]...)

			shouldFlush := false
			if !hasFlushed {
				shouldFlush = len(writeBuf) > 0 // 首包秒发
			} else {
				shouldFlush = len(writeBuf) >= currentThreshold || time.Since(lastFlush) >= maxLatency
			}

			if shouldFlush {
				if _, wErr := w.Write(writeBuf); wErr != nil {
					slog.Info("stream proxy client disconnected", "session", sessionID, "bytes", bytesRead)
					return nil
				}
				if canFlush {
					flusher.Flush()
				}
				bytesSinceLastUpdate += int64(len(writeBuf))
				writeBuf = writeBuf[:0]
				lastFlush = time.Now()
				hasFlushed = true
			}
		}

		if err != nil {
			// 流结束：刷出残余数据
			if len(writeBuf) > 0 {
				w.Write(writeBuf)
				if canFlush {
					flusher.Flush()
				}
			}
			if err == io.EOF {
				slog.Info("stream proxy upstream EOF", "session", sessionID, "bytes", bytesRead)
				return nil
			}
			slog.Error("stream proxy upstream error", "session", sessionID, "error", err, "bytes", bytesRead)
			return err
		}

		// 客户端断开检测
		select {
		case <-r.Context().Done():
			slog.Info("stream proxy client disconnected", "session", sessionID, "bytes", bytesRead)
			return nil
		default:
		}

		// 4. 每秒计算网速，进行动态换挡 (16,32,64,128,256,512,1024,2048)
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

			// 目标一秒吐流 4 次
			targetSize := int(bytesSinceLastUpdate / 4)
			newThreshold := 16 * 1024
			if targetSize >= 1024*1024 {
				newThreshold = 2048 * 1024
			} else if targetSize >= 512*1024 {
				newThreshold = 1024 * 1024
			} else if targetSize >= 256*1024 {
				newThreshold = 512 * 1024
			} else if targetSize >= 128*1024 {
				newThreshold = 256 * 1024
			} else if targetSize >= 64*1024 {
				newThreshold = 128 * 1024
			} else if targetSize >= 32*1024 {
				newThreshold = 64 * 1024
			} else if targetSize >= 16*1024 {
				newThreshold = 32 * 1024
			}

			// 约束在极限水位之间
			if newThreshold > maxThreshold {
				newThreshold = maxThreshold
			}

			if newThreshold != currentThreshold {
				// slog.Debug("stream proxy dynamic shift", "session", sessionID, "speed", bytesSinceLastUpdate, "old", currentThreshold, "new", newThreshold)
				currentThreshold = newThreshold
			}

			bytesSinceLastUpdate = 0
			lastUpdate = now
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

func ParseM3U(reader io.Reader) ([]map[string]string, string, error) {
	scanner := bufio.NewScanner(reader)
	// 增加 buffer 限制，防止某些 M3U 文件单行过长（如带有大尺寸 base64 logo url 时）超过 64KB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)
	var channels []map[string]string
	var current map[string]string
	var epgURL string

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
			epgURL = extractAttr(line, "x-tvg-url")
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
	return channels, epgURL, scanner.Err()
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
	attrs := []string{"tvg-id", "tvg-name", "tvg-logo", "group-title", "tvg-chno", "catchup", "catchup-source", "catchup-days", "fcc", "fcc-type"}
	for _, attr := range attrs {
		val := extractAttr(line, attr)
		if val != "" {
			ch[attr] = val
		}
	}
	return ch
}

// ParseM3UFile parses an M3U file from disk
func ParseM3UFile(path string) ([]map[string]string, string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	return ParseM3U(f)
}

func (sp *StreamProxy) openStreamTarget(ctx context.Context, targetURL string, ua string, headers map[string]string, ch *models.Channel) (*http.Response, error) {
	if strings.HasPrefix(targetURL, "udp://") || strings.HasPrefix(targetURL, "rtp://") {
		return sp.openUDPStreamWithFCC(ctx, targetURL, ch)
	}

	// 解析可能存在的 strm 直链
	resolvedURL, err := sp.resolveStrmUrl(ctx, targetURL, ua, headers, ch)
	if err == nil && resolvedURL != "" {
		targetURL = resolvedURL
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
		userAgents = []string{}
		if ua != "" {
			userAgents = append(userAgents, ua)
		}

		// 动态追加多个不同特征的兜底伪装
		userAgents = append(userAgents,
			"Mozilla/5.0 (Linux; Android 14; SM-S928B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.6099.230 Mobile Safari/537.36", // 移动端高频 Chrome
			"AppleCoreMedia/1.0.0.19G82 (Apple TV; U; CPU OS 15_6 like Mac OS X; en_us)",                                                  // Apple 原生 HLS 引擎，对于部分严格校验的 HLS/m3u8 源有奇效
			"ExoPlayerLib/2.19.1 (Linux; Android 14) ExoPlayerLib/2.19.1",                                                                 // 安卓标准播放引擎
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",       // 桌面端 macOS Safari
			"MediaPlayer-TV/1.0.0", // 本项目的专属兜底特征 UA
		)
	}

	// 获取带代理的 HTTP 客户端
	proxyClient := sp.getProxyClient(ch)

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

		rResp, rErr := proxyClient.Do(req)
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

func (sp *StreamProxy) openUDPStreamWithFCC(ctx context.Context, targetURL string, ch *models.Channel) (*http.Response, error) {
	var fccClient *multicast.FCCClient

	fccServer := ch.Fcc
	fccTypeStr := ch.FccType

	// Fallback to global settings if empty
	if fccServer == "" {
		globalEnabled, _ := sp.channelSvc.GetSetting("fcc_enabled")
		if globalEnabled == "true" {
			fccServer, _ = sp.channelSvc.GetSetting("fcc_default_server")
		}
	}

	if fccServer != "" {
		if fccTypeStr == "" {
			fccTypeStr, _ = sp.channelSvc.GetSetting("fcc_type")
			if fccTypeStr == "" {
				fccTypeStr = string(multicast.FccTypeTelecom)
			}
		}
		fccType := multicast.FccType(fccTypeStr)
		slog.Info("FCC Config Evaluated (openUDP)", "fccServer", fccServer, "fccType", fccTypeStr, "ch.Fcc", ch.Fcc, "ch.FccType", ch.FccType)

		portStart, portEnd := 40000, 40050
		pStart, _ := sp.channelSvc.GetSetting("fcc_port_start")
		pEnd, _ := sp.channelSvc.GetSetting("fcc_port_end")
		if pStart != "" {
			_, _ = fmt.Sscanf(pStart, "%d", &portStart)
		}
		if pEnd != "" {
			_, _ = fmt.Sscanf(pEnd, "%d", &portEnd)
		}

		fc, err := multicast.NewFCCClient(ctx, fccServer, portStart, portEnd, targetURL, fccType)
		if err != nil {
			slog.Warn("fcc initialization failed, falling back to pure multicast", "error", err)
		} else {
			fccClient = fc
		}
	}

	mreader, err := multicast.NewMulticastReader(ctx, targetURL, fccClient)
	if err != nil {
		return nil, fmt.Errorf("failed to join multicast: %w", err)
	}

	// We don't need a separate goroutine to close the reader, NewMulticastReader respects context.

	addrStr := strings.TrimPrefix(targetURL, "udp://")
	addrStr = strings.TrimPrefix(addrStr, "rtp://")
	addrStr = strings.TrimPrefix(addrStr, "@")
	reqURL, _ := url.Parse("udp://" + addrStr)

	resp := &http.Response{
		StatusCode: 200,
		Body:       mreader,
		Header:     make(http.Header),
		Request:    &http.Request{URL: reqURL},
	}
	resp.Header.Set("Content-Type", "video/mp2t")
	return resp, nil
}

// openLocalFile 打开本地文件并返回文件句柄、大小和 MIME 类型
func (sp *StreamProxy) openLocalFile(filePath string) (*os.File, int64, string, error) {
	cleanPath := filepath.Clean(filePath)
	if strings.Contains(cleanPath, "..") {
		return nil, 0, "", fmt.Errorf("路径不安全: 包含 '..'")
	}

	f, err := os.Open(cleanPath)
	if err != nil {
		return nil, 0, "", fmt.Errorf("打开本地文件失败: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, "", fmt.Errorf("获取文件信息失败: %w", err)
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, 0, "", fmt.Errorf("路径是一个目录，不是文件")
	}

	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(cleanPath)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	return f, info.Size(), contentType, nil
}

// serveLocalFileProxy 代理本地文件流，支持 HTTP Range 请求（拖动进度条）
func (sp *StreamProxy) serveLocalFileProxy(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel, filePath string) error {
	select {
	case sp.sem <- struct{}{}:
		defer func() { <-sp.sem }()
	default:
		return fmt.Errorf("并发流数已达上限 (%d)", sp.cfg.MaxConcurrent)
	}

	f, fileSize, contentType, err := sp.openLocalFile(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// 解析 Range 请求头，支持拖动进度条
	rangeHeader := r.Header.Get("Range")
	var start, end int64
	isRangeRequest := false

	if rangeHeader != "" && strings.HasPrefix(rangeHeader, "bytes=") {
		rangeSpec := strings.TrimPrefix(rangeHeader, "bytes=")
		parts := strings.SplitN(rangeSpec, "-", 2)
		if len(parts) == 2 {
			if parts[0] != "" {
				start, _ = strconv.ParseInt(parts[0], 10, 64)
			}
			if parts[1] != "" {
				end, _ = strconv.ParseInt(parts[1], 10, 64)
			} else {
				end = fileSize - 1 // "bytes=1000-" 表示从 1000 到文件末尾
			}
			if start >= 0 && start < fileSize && end >= start {
				isRangeRequest = true
			}
		}
	}

	sessionID := fmt.Sprintf("local-%d-%d-%d", channelID, clientID, time.Now().UnixNano())

	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		Mu:          &sync.RWMutex{},
		SessionID:   sessionID,
		ChannelID:   channelID,
		ChannelName: ch.Name,
		ClientID:    clientID,
		ClientName:  clientName,
		ClientIP:    clientIP,
		URL:         filePath,
		Status:      "playing_local",
		StartedAt:   time.Now(),
		LastActive:  time.Now(),
	}
	sp.cancels[sessionID] = func() {}
	sp.mu.Unlock()

	defer func() {
		sp.mu.Lock()
		delete(sp.streams, sessionID)
		delete(sp.cancels, sessionID)
		sp.mu.Unlock()
	}()

	// 设置公共响应头
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")

	if isRangeRequest {
		// 206 Partial Content —— Range 请求
		if end >= fileSize {
			end = fileSize - 1
		}
		contentLength := end - start + 1

		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
		w.WriteHeader(http.StatusPartialContent)

		if _, err := f.Seek(start, io.SeekStart); err != nil {
			slog.Error("local file seek failed", "session", sessionID, "seek_to", start, "error", err)
			return err
		}

		slog.Info("local file range request", "channel_id", channelID, "session", sessionID, "path", filePath, "range", fmt.Sprintf("%d-%d", start, end), "total", fileSize)

		// 数据泵：仅发送请求的字节范围
		bufPtr := streamBufferPool.Get().(*[]byte)
		buf := (*bufPtr)[:128*1024]
		defer streamBufferPool.Put(bufPtr)

		remaining := contentLength
		lastUpdate := time.Now()
		var bytesSinceLastUpdate int64

		for remaining > 0 {
			select {
			case <-r.Context().Done():
				return nil
			default:
			}

			toRead := int64(len(buf))
			if toRead > remaining {
				toRead = remaining
			}
			n, err := f.Read(buf[:toRead])
			if n > 0 {
				if _, wErr := w.Write(buf[:n]); wErr != nil {
					return nil
				}
				remaining -= int64(n)
				bytesSinceLastUpdate += int64(n)
			}
			if err != nil {
				return nil
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
		return nil
	}

	// 200 OK —— 全量请求（首次加载）
	w.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
	w.WriteHeader(http.StatusOK)

	types := strings.Split(ch.StreamType, "#")
	t := ""
	if len(types) > 0 {
		t = types[0]
	}
	flushThreshold := getFlushThreshold(t, filePath, filePath)
	flusher, canFlush := w.(http.Flusher)

	slog.Info("local file proxy started", "channel_id", channelID, "session", sessionID, "path", filePath, "size", fileSize, "threshold", flushThreshold)

	bufferSize := 128 * 1024
	if sp.cfg.BufferSize > bufferSize {
		bufferSize = sp.cfg.BufferSize
	}
	if bufferSize > 256*1024 {
		bufferSize = 256 * 1024
	}

	bufPtr := streamBufferPool.Get().(*[]byte)
	buf := (*bufPtr)[:bufferSize]
	defer streamBufferPool.Put(bufPtr)
	reader := bufio.NewReaderSize(f, bufferSize)

	writeBufPtr := streamBufferPool.Get().(*[]byte)
	writeBuf := (*writeBufPtr)[:0]
	defer streamBufferPool.Put(writeBufPtr)

	lastUpdate := time.Now()
	var bytesSinceLastUpdate int64
	hasFlushed := false

	for {
		select {
		case <-r.Context().Done():
			slog.Info("local file proxy client disconnected", "session", sessionID)
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
				slog.Info("local file proxy EOF", "session", sessionID)
				return nil
			}
			slog.Error("local file proxy read error", "session", sessionID, "error", err)
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

func (sp *StreamProxy) serveMulticastProxy(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel, targetURL string) error {
	sessionID := fmt.Sprintf("multicast-%d-%s", channelID, uuid.New().String())

	slog.Info("starting multicast proxy", "channel_id", channelID, "url", targetURL)

	// Catch-up / Time-shift Bypass
	isCatchup := r.URL.Query().Get("playseek") != "" || r.URL.Query().Get("starttime") != "" || r.URL.Query().Get("catchup") != ""
	isLiveMulticast := strings.HasPrefix(targetURL, "udp://") || strings.HasPrefix(targetURL, "rtp://")

	var fccClient *multicast.FCCClient
	if !isCatchup && isLiveMulticast {
		// 1. Parse FCC parameters from targetURL (channel URL)
		var fccServer, fccTypeStr string
		if parsedURL, err := url.Parse(targetURL); err == nil {
			fccServer = parsedURL.Query().Get("fcc")
			fccTypeStr = parsedURL.Query().Get("fcc-type")
		}

		// 2. Override with HTTP request query parameters if present
		if reqFcc := r.URL.Query().Get("fcc"); reqFcc != "" {
			fccServer = reqFcc
			fccTypeStr = r.URL.Query().Get("fcc-type")
		}

		// 3. Fallback to channel settings if still empty
		if fccServer == "" && ch != nil && ch.Fcc != "" {
			fccServer = ch.Fcc
			fccTypeStr = ch.FccType
		}

		// 4. Fallback to global settings
		if fccServer == "" {
			globalEnabled, _ := sp.channelSvc.GetSetting("fcc_enabled")
			if globalEnabled == "true" {
				fccServer, _ = sp.channelSvc.GetSetting("fcc_default_server")
			}
		}

		if fccServer != "" {
			if fccTypeStr == "" {
				fccTypeStr, _ = sp.channelSvc.GetSetting("fcc_type")
				if fccTypeStr == "" {
					fccTypeStr = string(multicast.FccTypeTelecom)
				}
			}
			fccType := multicast.FccType(fccTypeStr)

			// 获取公网IP配置
			publicIP, _ := sp.channelSvc.GetSetting("fcc_public_ip")

			slog.Info("FCC Config Evaluated", "fccServer", fccServer, "fccType", fccTypeStr, "ch.FccType", ch.FccType, "publicIP", publicIP)

			portStart, portEnd := 40000, 40050
			pStart, _ := sp.channelSvc.GetSetting("fcc_port_start")
			pEnd, _ := sp.channelSvc.GetSetting("fcc_port_end")
			if pStart != "" {
				_, _ = fmt.Sscanf(pStart, "%d", &portStart)
			}
			if pEnd != "" {
				_, _ = fmt.Sscanf(pEnd, "%d", &portEnd)
			}

			// Try to connect FCC
			fc, err := multicast.NewFCCClient(r.Context(), fccServer, portStart, portEnd, targetURL, fccType, publicIP)
			if err != nil {
				slog.Warn("fcc initialization failed, falling back to pure multicast", "error", err)
			} else {
				fccClient = fc
			}
		}
	}

	reader, err := multicast.NewMulticastReader(r.Context(), targetURL, fccClient)
	if err != nil {
		return fmt.Errorf("failed to open multicast reader: %w", err)
	}
	defer func() { _ = reader.Close() }()

	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("X-Stream-Type", "multicast")
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

	// Track RTP sequence numbers and fix TS continuity counters on packet loss
	tsCCFixer := multicast.NewTsCCFixer()
	var prevSeq uint16
	var hasSeq bool

	c.OnPacketRTPAny(func(medi *description.Media, forma format.Format, pkt *rtp.Packet) {
		// Detect RTP packet loss via sequence number gap
		packetLossDetected := false
		if hasSeq {
			expectedSeq := prevSeq + 1
			if pkt.SequenceNumber != expectedSeq {
				packetLossDetected = true
				slog.Debug("rtsp proxy RTP packet loss detected",
					"channel_id", channelID,
					"expected", expectedSeq,
					"received", pkt.SequenceNumber,
					"diff", pkt.SequenceNumber-expectedSeq)
			}
		}
		prevSeq = pkt.SequenceNumber
		hasSeq = true

		// Fix TS continuity counters to prevent TsExtractor freezing
		tsCCFixer.Process(pkt.Payload, packetLossDetected)

		// Just strip RTP header and write payload assuming it's TS over RTSP or playable raw payload
		n, wErr := w.Write(pkt.Payload)
		if wErr != nil {
			c.Close() // Force stop on client disconnect
			return
		}

		bytesSinceLastUpdate += int64(n)

		now := time.Now()
		if now.Sub(lastUpdate) >= time.Millisecond*100 {
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
