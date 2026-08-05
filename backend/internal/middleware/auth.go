package middleware

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/mediaplayer/backend/internal/utils"
)

var publicPaths = map[string]bool{
	"/api/v1/client/register":             true,
	"/api/v1/client/verify":               true,
	"/api/v1/admin/login":                 true,
	"/api/v1/admin/system/db_snapshot":    true,
	"/api/v1/admin/system/logos_snapshot": true,
	"/ping":                               true,
}

// 已经取消只读公开接口，全部强制鉴权以防止防盗链失效
var readOnlyPublicPaths = map[string]bool{}

// AuthMiddleware 支持两种认证方式：
// 1. Admin JWT token (Bearer xxx) → 完整管理权限
// 2. Client access token (通过 X-Client-Token 或 Authorization) → 受限访问
func AuthMiddleware(secret string, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		auth := c.GetHeader("Authorization")
		clientToken := c.GetHeader("X-Client-Token")

		// 完全公开接口（无需认证）
		if publicPaths[path] {
			c.Next()
			return
		}

		// 尝试 Admin JWT 认证
		jwtFailed := false
		if auth != "" {
			tokenStr := strings.TrimPrefix(auth, "Bearer ")
			// 格式预检：JWT 由 3 段 base64 编码组成，以点分隔（header.payload.signature）
			// 非 JWT 格式的 token 直接跳过解析，避免不必要的开销
			if strings.Count(tokenStr, ".") == 2 {
				token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
					return []byte(secret), nil
				})
				if err == nil && token.Valid {
					c.Set("auth_type", "admin")
					c.Set("operator", "admin")
					c.Next()
					return
				}
				// 格式符合 JWT 但解析失败，不降级为 client token 查询
				jwtFailed = true
			}
		}

		// 尝试 Client Token 认证
		token := clientToken
		if token == "" && !jwtFailed {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		// 流代理场景：开发调试开关 (允许通过 URL query 传递 token)
		if token == "" && db != nil {
			var enableUrlToken string
			err := db.QueryRow(`SELECT value FROM user_settings WHERE key='enable_url_token'`).Scan(&enableUrlToken)
			if err != nil && err != sql.ErrNoRows {
				slog.Warn("查询 enable_url_token 失败", "error", err)
			}
			if enableUrlToken == "true" {
				token = c.Query("token")
				if token != "" {
					slog.Warn("token 通过 URL query 传递 (调试模式已开启)",
						"path", c.Request.URL.Path,
						"ip", utils.GetRealClientIP(c),
					)
				}
			} else if c.Query("plan") == "1" {
				// 如果开启了外部订阅，允许外部订阅流地址带有 plan=1 时，从 URL 提取 token 进行播放。
				var enableExternalSub string
				err := db.QueryRow(`SELECT value FROM user_settings WHERE key='enable_external_sub'`).Scan(&enableExternalSub)
				if err != nil && err != sql.ErrNoRows {
					slog.Warn("查询 enable_external_sub 失败", "error", err)
				}
				if enableExternalSub == "true" {
					token = c.Query("token")
					if token != "" {
						c.Set("is_sub_token_only", true)
					}
				}
			}
		}

		// 针对 VLC 等不支持自定义 Authorization 头的播放器，尝试从 User-Agent 提取 (Token=xxx)
		if token == "" {
			ua := c.GetHeader("User-Agent")
			if strings.Contains(ua, "(Token=") {
				parts := strings.Split(ua, "(Token=")
				if len(parts) > 1 {
					extracted := strings.Split(parts[1], ")")[0]
					token = strings.TrimSpace(extracted)
				}
			}
		}

		if token != "" && db != nil {
			isSubTokenOnly := c.GetBool("is_sub_token_only")

			if !isSubTokenOnly {
				var clientID int64
				var status string
				var name string
				err := db.QueryRow(`SELECT id, status, name FROM clients WHERE access_token=?`, token).Scan(&clientID, &status, &name)
				if err == nil {
					if status != "approved" {
						c.JSON(http.StatusForbidden, gin.H{
							"code":    403,
							"message": "客户端未授权",
						})
						c.Abort()
						return
					}
					c.Set("auth_type", "client")
					c.Set("client_id", clientID)
					c.Set("client_name", name)
					c.Set("client_token", token)
					c.Next()
					return
				}
			}

			// 如果不是合法的客户端 Token (或仅限订阅校验)，校验是否为合法的订阅 Token (允许第三方播放器通过订阅访问代理流)
			var planID int64
			err := db.QueryRow(`SELECT id FROM subscription_plans WHERE subscription_token=?`, token).Scan(&planID)
			if err == nil {
				c.Set("auth_type", "client")
				c.Set("client_token", token)
				c.Next()
				return
			}

			if isSubTokenOnly {
				c.JSON(http.StatusForbidden, gin.H{
					"code":    403,
					"message": "无效的订阅 Token",
				})
				c.Abort()
				return
			}
		}

		// 未认证请求：仅允许特定只读 GET 接口
		if c.Request.Method == "GET" && isReadOnlyPublic(path) {
			c.Next()
			return
		}

		// 其余接口必须认证
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    401,
			"message": "需要认证 (Admin JWT 或 Client Token)",
		})
		c.Abort()
	}
}

// isReadOnlyPublic 检查路径是否为只读公开接口
func isReadOnlyPublic(path string) bool {
	if readOnlyPublicPaths[path] {
		return true
	}
	// 支持前缀匹配（如 /api/v1/channels/123）
	for prefix := range readOnlyPublicPaths {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// RequireAdmin 要求管理员认证 (用于管理端接口)
func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		authType, _ := c.Get("auth_type")
		if authType != "admin" {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "需要管理员权限",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireClientAuth 要求客户端认证 (用于客户端专属接口)
func RequireClientAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authType, _ := c.Get("auth_type")
		if authType != "client" && authType != "admin" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "需要客户端认证",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ── 速率限制 ───────────────────────────────────────────

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rate     int           // 每窗口允许的请求数
	window   time.Duration // 窗口大小
	maxSize  int
}

type visitor struct {
	count    int
	lastSeen time.Time
}

var loginLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     5,               // 5 次
	window:   1 * time.Minute, // 每分钟
	maxSize:  10000,
}

var clientAuthLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     60, // 客户端注册/验证接口限流：每分钟 60 次
	window:   1 * time.Minute,
	maxSize:  50000,
}

var apiLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     300,             // 放宽至 300 次/分钟，防止管理后台正常刷新被误伤
	window:   1 * time.Minute, // 每分钟
	maxSize:  50000,
}

var logoLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     600, // 台标请求量大但轻量，600 次/分钟
	window:   1 * time.Minute,
	maxSize:  50000,
}

var streamLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     60, // 流代理播放，正常换台不会超过 60 次/分钟
	window:   1 * time.Minute,
	maxSize:  50000,
}

// InitRateLimiters 从配置覆盖限流速率值（由 main 启动时调用）
// 传入值 <= 0 时保持原有默认值不变
func InitRateLimiters(apiRate, logoRate, streamRate int) {
	if apiRate > 0 {
		apiLimiter.rate = apiRate
		slog.Info("rate limit configured", "limiter", "api", "rate", apiRate)
	}
	if logoRate > 0 {
		logoLimiter.rate = logoRate
		slog.Info("rate limit configured", "limiter", "logo", "rate", logoRate)
	}
	if streamRate > 0 {
		streamLimiter.rate = streamRate
		slog.Info("rate limit configured", "limiter", "stream", "rate", streamRate)
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > rl.window {
		if !exists && len(rl.visitors) >= rl.maxSize {
			rl.cleanupLocked(now)
			// 如果清理过期记录后依然满载，说明遇到了海量伪造 IP 的 CC 攻击
			// 此时为了自保并防止合法用户被拦截，随机剔除一个旧元素 (利用 Go map 的随机遍历特性, O(1) 开销)
			if len(rl.visitors) >= rl.maxSize {
				for k := range rl.visitors {
					delete(rl.visitors, k)
					break
				}
			}
		}
		rl.visitors[key] = &visitor{count: 1, lastSeen: now}
		return true
	}

	if v.count >= rl.rate {
		return false
	}

	v.count++
	v.lastSeen = now
	return true
}

func (rl *rateLimiter) cleanupLocked(now time.Time) {
	for k, v := range rl.visitors {
		if now.Sub(v.lastSeen) > rl.window*2 {
			delete(rl.visitors, k)
		}
	}
}

// 定期清理过期 visitor（由 main 中调用）
func (rl *rateLimiter) Cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.cleanupLocked(time.Now())
}

// LoginRateLimit 登录接口限流（每 IP 每分钟 5 次）
func LoginRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := utils.GetRealClientIP(c)
		if !loginLimiter.allow(ip) {
			slog.Warn("rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// APIRateLimit 全局 API 限流（每 IP 每分钟 300 次）
func APIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := utils.GetRealClientIP(c)
		if !apiLimiter.allow(ip) {
			slog.Warn("API rate limit exceeded", "ip", ip)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ClientAuthRateLimit 客户端注册与验证接口限流（每 IP 每分钟 60 次）
func ClientAuthRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := utils.GetRealClientIP(c)
		if !clientAuthLimiter.allow(ip) {
			slog.Warn("client auth rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// LogoRateLimit 台标接口限流（每 IP 每分钟 600 次），独立于 API 限流
func LogoRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := utils.GetRealClientIP(c)
		if !logoLimiter.allow(ip) {
			slog.Warn("logo rate limit exceeded", "ip", ip)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "台标请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// StreamRateLimit 流代理接口限流（每 IP 每分钟 60 次），独立于 API 限流
func StreamRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := utils.GetRealClientIP(c)
		if !streamLimiter.allow(ip) {
			slog.Warn("stream rate limit exceeded", "ip", ip, "path", c.Request.URL.Path)
			c.JSON(http.StatusTooManyRequests, gin.H{
				"code":    429,
				"message": "流媒体请求过于频繁，请稍后再试",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// StartRateLimitCleanup 启动定期清理协程
func StartRateLimitCleanup(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			loginLimiter.Cleanup()
			apiLimiter.Cleanup()
			logoLimiter.Cleanup()
			streamLimiter.Cleanup()
		}
	}
}

// ── 结构化日志 ─────────────────────────────────────────

// Logger 返回基于 slog 的结构化日志中间件
func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := utils.GetRealClientIP(c)
		method := c.Request.Method

		// 检测是否获取到真实 IP：如果 IP 是回环地址或私有地址，且有反向代理头，说明配置可能有问题
		if clientIP == "127.0.0.1" || clientIP == "::1" {
			if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
				slog.Warn("检测到 X-Forwarded-For 头但 client_ip 为回环地址，请检查 server.trusted_proxies 配置",
					"client_ip", clientIP, "x_forwarded_for", xff, "x_real_ip", c.GetHeader("X-Real-IP"))
			}
		}

		attrs := []slog.Attr{
			slog.String("client_ip", clientIP),
			slog.String("method", method),
			slog.String("path", path),
			slog.Int("status", status),
			slog.Duration("latency", latency),
			slog.Int("body_size", c.Writer.Size()),
		}
		if query != "" {
			attrs = append(attrs, slog.String("query", query))
		}
		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("errors", c.Errors.String()))
		}

		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}

		slog.LogAttrs(c.Request.Context(), level, "HTTP", attrs...)
	}
}
