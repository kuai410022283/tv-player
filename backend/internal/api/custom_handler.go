package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mediaplayer/backend/internal/services"
)

type CustomHandler struct {
	customSvc *services.CustomService
}

func NewCustomHandler(customSvc *services.CustomService) *CustomHandler {
	return &CustomHandler{customSvc: customSvc}
}

// GET /api/v1/admin/custom/status
func (h *CustomHandler) GetStatus(c *gin.Context) {
	status := h.customSvc.GetEnvStatus()
	ok(c, status)
}

// POST /api/v1/admin/custom/setup-env
func (h *CustomHandler) SetupEnv(c *gin.Context) {
	var req struct {
		ProxyURL string `json:"proxy_url"`
	}
	// 柔性解析，即使没有传 body 也不会崩
	_ = c.ShouldBindJSON(&req)

	if err := h.customSvc.SetupEnvironment(req.ProxyURL); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"message": "环境部署任务启动成功"})
}

// POST /api/v1/admin/custom/cancel-setup
func (h *CustomHandler) CancelSetup(c *gin.Context) {
	h.customSvc.CancelSetupEnvironment()
	ok(c, gin.H{"message": "已成功停止部署下载任务"})
}

// GET /api/v1/admin/custom/settings
func (h *CustomHandler) GetSettings(c *gin.Context) {
	settings, err := h.customSvc.GetSettings()
	if err != nil {
		fail(c, 500, "加载定制设置失败: "+err.Error())
		return
	}
	ok(c, settings)
}

// POST /api/v1/admin/custom/settings
func (h *CustomHandler) SaveSettings(c *gin.Context) {
	var settings services.CustomSettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		fail(c, 400, "参数格式错误: "+err.Error())
		return
	}
	if err := h.customSvc.SaveSettings(settings); err != nil {
		fail(c, 500, "保存设置失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "保存成功"})
}

// POST /api/v1/admin/custom/upload-jks
func (h *CustomHandler) UploadJks(c *gin.Context) {
	file, err := c.FormFile("jks")
	if err != nil {
		fail(c, 400, "获取上传文件失败")
		return
	}
	src, err := file.Open()
	if err != nil {
		fail(c, 500, "打开文件失败")
		return
	}
	defer src.Close()

	if err := h.customSvc.SaveUserJks(src); err != nil {
		fail(c, 500, "保存证书失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "证书上传成功"})
}

// POST /api/v1/admin/custom/upload-logo
func (h *CustomHandler) UploadLogo(c *gin.Context) {
	file, err := c.FormFile("logo")
	if err != nil {
		fail(c, 400, "获取上传文件失败")
		return
	}
	src, err := file.Open()
	if err != nil {
		fail(c, 500, "打开文件失败")
		return
	}
	defer src.Close()

	if err := h.customSvc.SaveUserLogo(src); err != nil {
		fail(c, 500, "保存图标失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "应用图标上传成功"})
}

// POST /api/v1/admin/custom/build
func (h *CustomHandler) Build(c *gin.Context) {
	if err := h.customSvc.StartBuildPackage(); err != nil {
		fail(c, 400, err.Error())
		return
	}
	ok(c, gin.H{"message": "APK 定制打包任务已启动，正在后台生成中"})
}

// GET /api/v1/admin/custom/build-log
func (h *CustomHandler) GetBuildLog(c *gin.Context) {
	logs := h.customSvc.GetBuildLog()
	c.String(http.StatusOK, logs)
}
