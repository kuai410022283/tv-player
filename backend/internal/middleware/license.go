package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/license"
)

// RequireVIP 检查服务器是否已激活 VIP 授权。
// 未授权时返回 403，前端根据此状态隐藏相关 UI。
func RequireVIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !license.IsActivated() {
			c.JSON(http.StatusForbidden, gin.H{
				"code":    403,
				"message": "未授权，请联系管理员获取授权码",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}