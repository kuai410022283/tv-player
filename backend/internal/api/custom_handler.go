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
	defer func() { _ = src.Close() }()

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
	defer func() { _ = src.Close() }()

	if err := h.customSvc.SaveUserLogo(src); err != nil {
		fail(c, 500, "保存图标失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "应用图标上传成功"})
}

// POST /api/v1/admin/custom/upload-banner
func (h *CustomHandler) UploadBanner(c *gin.Context) {
	file, err := c.FormFile("banner")
	if err != nil {
		fail(c, 400, "获取上传文件失败")
		return
	}
	src, err := file.Open()
	if err != nil {
		fail(c, 500, "打开文件失败")
		return
	}
	defer func() { _ = src.Close() }()

	if err := h.customSvc.SaveUserBanner(src); err != nil {
		fail(c, 500, "保存 TV 横幅失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "Android TV 宽屏横幅上传成功"})
}

// POST /api/v1/admin/custom/build
func (h *CustomHandler) Build(c *gin.Context) {
	// 接收可选的表单参数，自动保存后再启动打包，确保使用最新配置
	var settings services.CustomSettings
	if err := c.ShouldBindJSON(&settings); err == nil {
		// 前端传了参数则先保存
		if settings.AppName != "" || settings.VersionName != "" || settings.VersionCode > 0 {
			if err := h.customSvc.SaveSettings(settings); err != nil {
				fail(c, 500, "保存设置失败: "+err.Error())
				return
			}
		}
	}
	// 无论如何都忽略绑定错误（可能没传 body），直接用 DB 中已有设置

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

// POST /api/v1/admin/custom/reset-env
func (h *CustomHandler) ResetEnv(c *gin.Context) {
	if err := h.customSvc.ResetEnvironment(); err != nil {
		fail(c, 500, "清理依赖失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "打包环境依赖已清理，可重新部署"})
}

// GET /api/v1/admin/custom/file-status
func (h *CustomHandler) GetFileStatus(c *gin.Context) {
	files := h.customSvc.GetUploadedFileStatus()
	ok(c, files)
}

// DELETE /api/v1/admin/custom/uploaded-file
func (h *CustomHandler) DeleteUploadedFile(c *gin.Context) {
	fileType := c.Query("type")
	if fileType == "" {
		fail(c, 400, "请指定文件类型 (jks/logo/banner)")
		return
	}
	if err := h.customSvc.DeleteUploadedFile(fileType); err != nil {
		fail(c, 500, "删除文件失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "文件已删除"})
}

// GET /api/v1/admin/custom/download-versions
func (h *CustomHandler) ListDownloadVersions(c *gin.Context) {
	versions := h.customSvc.ListDownloadVersions()
	ok(c, versions)
}

// DELETE /api/v1/admin/custom/download-versions/:dir
func (h *CustomHandler) DeleteDownloadVersion(c *gin.Context) {
	dir := c.Param("dir")
	if err := h.customSvc.DeleteDownloadVersion(dir); err != nil {
		fail(c, 500, "删除版本目录失败: "+err.Error())
		return
	}
	ok(c, gin.H{"message": "版本目录已删除"})
}
