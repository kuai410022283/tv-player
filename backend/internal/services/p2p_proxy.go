package services

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

// serveP2pProxy 处理 p2p:// (ForceTech P2P) 协议代理，转发给本地/远程 ForceTech 守护进程并以 HTTP-TS 流输出
func (sp *StreamProxy) serveP2pProxy(channelID int64, _ int64, _ string, _ string, w http.ResponseWriter, r *http.Request, _ *models.Channel, targetURL string) error {
	slog.Info("开始 ForceTech P2P 代理", "channel_id", channelID, "target_url", targetURL)

	httpURL, err := buildForceTechHttpURL(targetURL)
	if err != nil {
		return fmt.Errorf("解析 p2p:// URL 失败: %w", err)
	}

	return sp.forwardP2pStream(r.Context(), w, httpURL, "p2p (ForceTech)")
}

// serveTvbusProxy 处理 tvbus:// (TVBus P2P) 协议代理，转发给本地/远程 TVBus 守护进程并以 HTTP-TS 流输出
func (sp *StreamProxy) serveTvbusProxy(channelID int64, _ int64, _ string, _ string, w http.ResponseWriter, r *http.Request, _ *models.Channel, targetURL string) error {
	slog.Info("开始 TVBus P2P 代理", "channel_id", channelID, "target_url", targetURL)

	httpURL, err := buildTvbusHttpURL(targetURL)
	if err != nil {
		return fmt.Errorf("解析 tvbus:// URL 失败: %w", err)
	}

	return sp.forwardP2pStream(r.Context(), w, httpURL, "tvbus (TVBus)")
}

// buildForceTechHttpURL 将 p2p://<host:port>/<hash> 或 p2p://<hash> 转换为 P2P 守护进程的 HTTP 接口地址
func buildForceTechHttpURL(rawURL string) (string, error) {
	trimmed := strings.TrimPrefix(rawURL, "p2p://")
	if trimmed == "" {
		return "", fmt.Errorf("无效的 p2p:// 链接")
	}

	// 格式1: p2p://127.0.0.1:9906/1234567890abcdef...
	// 格式2: p2p://1234567890abcdef... (默认连接本地 9906 或 P2P_FORCETECH_PORT 环境变量)
	if strings.Contains(trimmed, "/") {
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) == 2 && parts[0] != "" {
			return fmt.Sprintf("http://%s/%s", parts[0], parts[1]), nil
		}
	}

	defaultPort := os.Getenv("P2P_FORCETECH_PORT")
	if defaultPort == "" {
		defaultPort = "9906"
	}
	return fmt.Sprintf("http://127.0.0.1:%s/%s", defaultPort, trimmed), nil
}

// buildTvbusHttpURL 将 tvbus://<channel_id> 转换为 TVBus 守护进程的 HTTP 接口地址
func buildTvbusHttpURL(rawURL string) (string, error) {
	trimmed := strings.TrimPrefix(rawURL, "tvbus://")
	if trimmed == "" {
		return "", fmt.Errorf("无效的 tvbus:// 链接")
	}

	// 如果包含 http 路径形式或带有自定义端口的 host:port，直接提取
	if strings.HasPrefix(trimmed, "http://") || strings.Contains(trimmed, "/") {
		u, err := url.Parse("http://" + strings.TrimPrefix(trimmed, "http://"))
		if err == nil && u.Host != "" {
			return u.String(), nil
		}
	}

	defaultPort := os.Getenv("P2P_TVBUS_PORT")
	if defaultPort == "" {
		defaultPort = "8902"
	}
	return fmt.Sprintf("http://127.0.0.1:%s/live.ts?id=%s", defaultPort, url.QueryEscape(trimmed)), nil
}

// forwardP2pStream 统一拉取 P2P 守护进程的 HTTP 数据流并高效透传写给 ResponseWriter
func (sp *StreamProxy) forwardP2pStream(ctx context.Context, w http.ResponseWriter, httpURL string, p2pName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
	if err != nil {
		return fmt.Errorf("创建 %s P2P HTTP 请求失败: %w", p2pName, err)
	}

	req.Header.Set("User-Agent", "MediaPlayer-TV/1.0.0 P2P-Proxy")

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接 %s 守护服务 (%s) 失败，请检查 P2P 守护进程是否已启动: %w", p2pName, httpURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 守护服务返回异常状态码: %d", p2pName, resp.StatusCode)
	}

	// 设置响应头：默认以 MPEG-TS 输出，开启分块传输
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "video/mp2t"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}

	// 大块内存池 Flush 传输，提升吞吐抗卡顿
	bufPtr := streamBufferPool.Get().(*[]byte)
	defer streamBufferPool.Put(bufPtr)
	buf := *bufPtr

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return wErr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}
