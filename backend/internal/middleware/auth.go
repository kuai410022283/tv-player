package middleware

import (
	"database/sql"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// AuthMiddleware 支持两种认证方式：
// 1. Admin JWT token (Bearer xxx) → 完整管理权限
// 2. Client access token (通过 X-Client-Token 或 Authorization) → 受限访问
func AuthMiddleware(secret string, db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		clientToken := c.GetHeader("X-Client-Token")

		// GET 请求允许匿名访问 (只读)
		if c.Request.Method == "GET" && auth == "" && clientToken == "" {
			c.Next()
			return
		}

		// 方式1: Admin JWT
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

		// 方式2: Client access token
		token := clientToken
		if token == "" {
			token = strings.TrimPrefix(auth, "Bearer ")
		}

		if token != "" && db != nil {
			var clientID int64
			var status string
			err := db.QueryRow(`SELECT id, status FROM clients WHERE access_token=?`, token).Scan(&clientID, &status)
			if err == nil {
				if status != "approved" {
					c.JSON(http.StatusForbidden, gin.H{
						"code":    403,
						"message": "客户端未授权: " + status,
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

		// 写操作必须认证
		if c.Request.Method != "GET" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"code":    401,
				"message": "需要认证 (Admin JWT 或 Client Token)",
			})
			c.Abort()
			return
		}

		// GET 请求降级为匿名
		c.Next()
	}
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

// ClientRateLimit 客户端速率限制 (简单实现)
func ClientRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 简单的基于IP的速率限制
		// 生产环境建议使用 redis 或专用限流库
		c.Next()
	}
}

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		c.Next()
		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		log.Printf("[API] %s %s %s %d %v", clientIP, method, path, status, latency)
	}
}
