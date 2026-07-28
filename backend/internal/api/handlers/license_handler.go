package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/license"
)

// LicenseHandler 处理授权相关 API 请求
type LicenseHandler struct{}

// NewLicenseHandler 创建 LicenseHandler
func NewLicenseHandler() *LicenseHandler {
	return &LicenseHandler{}
}

// GetStatus 获取当前授权状态
// GET /api/v1/admin/license/status
func (h *LicenseHandler) GetStatus(c *gin.Context) {
	info := license.GetInfo()
	c.JSON(http.StatusOK, gin.H{
		"code": 200,
		"data": info,
	})
}

// Activate 激活授权码
// POST /api/v1/admin/license/activate
func (h *LicenseHandler) Activate(c *gin.Context) {
	var req struct {
		LicenseKey string `json:"license_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": "请提供授权码",
		})
		return
	}

	info, err := license.Activate(req.LicenseKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code":    400,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "授权激活成功",
		"data":    info,
	})
}

// Revoke 吊销当前授权
// POST /api/v1/admin/license/revoke
func (h *LicenseHandler) Revoke(c *gin.Context) {
	if err := license.Revoke(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    500,
			"message": "吊销失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    200,
		"message": "授权已吊销",
	})
}