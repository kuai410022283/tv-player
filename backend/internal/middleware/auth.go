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
)

// 公开接口白名单 — 这些接口无需任何认证
var publicPaths = map[string]bool{
	"/api/v1/client/register": true,
	"/api/v1/client/verify":   true,
	"/api/v1/admin/login":     true,
	"/ping":                   true,
}

// 只读公开接口 — GET 请求允许匿名访问（频道列表等供未注册设备浏览）
var readOnlyPublicPaths = map[string]bool{
	"/api/v1/channels":  true,
	"/api/v1/channels/": true, // 前缀匹配用
	"/api/v1/groups":    true,
	"/api/v1/epg":       true,
}

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
		if auth != "" {
			tokenStr := strings.TrimPrefix(auth, "Bearer ")
			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
				return []byte(secret), nil
			})
			if err == nil && token.Valid {
				c.Set("auth_type", "admin")
				c.Set("operator", "admin")
				c.Next()
				return
			}
		}

		// 尝试 Client Token 认证
		token := clientToken
		if token == "" {
			token = strings.TrimPrefix(auth, "Bearer ")
		}
		// 流代理场景：通过 query 参数传递 token（已废弃，建议用 Header）
		if token == "" {
			token = c.Query("token")
			if token != "" {
				slog.Warn("token 通过 URL query 传递已废弃，请使用 Header",
					"path", c.Request.URL.Path,
					"ip", c.ClientIP(),
				)
			}
		}
		if token != "" && db != nil {
			var clientID int64
			var status string
			err := db.QueryRow(`SELECT id, status FROM clients WHERE access_token=?`, token).Scan(&clientID, &status)
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
				c.Set("client_token", token)
				c.Next()
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
	rate:     5,              // 5 次
	window:   1 * time.Minute, // 每分钟
	maxSize:  10000,
}

var apiLimiter = &rateLimiter{
	visitors: make(map[string]*visitor),
	rate:     60,             // 60 次
	window:   1 * time.Minute, // 每分钟
	maxSize:  50000,
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[key]
	now := time.Now()

	if !exists || now.Sub(v.lastSeen) > rl.window {
		if !exists && len(rl.visitors) >= rl.maxSize {
			rl.cleanupLocked(now)
			if len(rl.visitors) >= rl.maxSize {
				return false
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
		ip := c.ClientIP()
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

// APIRateLimit 全局 API 限流（每 IP 每分钟 60 次）
func APIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
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
		clientIP := c.ClientIP()
		method := c.Request.Method

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
