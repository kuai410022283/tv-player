package mobile

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/mediaplayer/backend/internal/config"
	"github.com/mediaplayer/backend/internal/services"
)

var (
	streamProxy *services.StreamProxy
)

// StartLocalProxy 启动客户端本地的 HTTP 到 UDP/RTSP 代理层
// 这个方法由 Android 的 JNI (gomobile) 调用
func StartLocalProxy(port int) error {
	// 初始化简易日志
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("Starting local Go proxy engine...", "port", port)

	// 在本地初始一个纯内存的无状态 SQLite，用来满足 ChannelService 依赖，防止空指针
	db, err := services.InitDB(":memory:")
	if err != nil {
		return fmt.Errorf("failed to init memory db: %w", err)
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

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	slog.Info("Local proxy listening on", "addr", addr)

	// 后台运行，不要阻塞主线程，因为这是在 Android 进程里
	go func() {
		if err := http.ListenAndServe(addr, mux); err != nil {
			slog.Error("Local proxy server crashed", "error", err)
		}
	}()

	return nil
}
