package mobile

import (
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/services"
)

var (
	streamProxy  *services.StreamProxy
	proxyStarted bool
)

// StartLocalProxy 启动客户端本地的 HTTP 到 UDP/RTSP 代理层
// 自动分配随机端口，返回端口号（失败返回 -1）
// 这个方法由 Android 的 JNI (gomobile) 调用
func StartLocalProxy() int {
	// 防止重复启动
	if proxyStarted {
		return -1
	}

	// 初始化简易日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("Starting local Go proxy engine...")

	// 在本地初始一个纯内存的无状态 SQLite，用来满足 ChannelService 依赖，防止空指针
	db, err := services.InitDB(":memory:")
	if err != nil {
		slog.Error("Failed to init memory db", "error", err)
		return -1
	}

	channelSvc := services.NewChannelService(db)

	cfg := &config.StreamConfig{
		MaxConcurrent: 50,
		CacheDir:      "/tmp/proxy_cache", // Android 端不实际落盘
	}

	streamProxy = services.NewStreamProxy(cfg, channelSvc)

	// 启动一个精简版的 HTTP 服务
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy", func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("proxy handler panic recovered", "panic", rec)
			}
		}()
		// 提取 url 参数
		targetURL := r.URL.Query().Get("url")
		if targetURL == "" {
			http.Error(w, "missing url parameter", http.StatusBadRequest)
			return
		}

		// 使用 StreamProxy 直接进行流转，绕过数据库
		err := streamProxy.ServeLocalProxy(w, r, targetURL)
		if err != nil {
			slog.Error("proxy error", "url", targetURL, "error", err)
		}
	})

	// 自动分配随机端口，避免冲突
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		slog.Error("Failed to listen", "error", err)
		return -1
	}
	port := listener.Addr().(*net.TCPAddr).Port
	slog.Info("Local proxy listening on", "port", port)

	// 后台运行，使用 listener 直接 Serve，无竞态风险
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("local proxy server panic recovered", "panic", rec)
			}
		}()
		if err := http.Serve(listener, mux); err != nil {
			slog.Error("Local proxy server crashed", "error", err)
		}
	}()

	proxyStarted = true
	return port
}
