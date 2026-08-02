package services

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"database/sql"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ToolStatus 表示单个文件的就绪/下载状态
type ToolStatus struct {
	Name     string `json:"name"`
	Exists   bool   `json:"exists"`
	Progress int    `json:"progress"` // 0-100
	Error    string `json:"error,omitempty"`
}

// BaseVersionInfo 官方底本信息
type BaseVersionInfo struct {
	Version string `json:"version"` // "v1.1.16"
	Code    int    `json:"code"`    // 116
	Dir     string `json:"dir"`     // "116_v1.1.16"
}

// EnvStatus 表示整体打包环境的状态
type EnvStatus struct {
	Arch           string            `json:"arch"`           // amd64 / arm64 / arm
	ToolsReady     bool              `json:"tools_ready"`    // 编译工具链是否全部就绪
	Downloading    bool              `json:"downloading"`    // 是否正在下载中
	BaseApkReady   bool              `json:"base_apk_ready"` // 官方底本是否存在
	BaseApkPath    string            `json:"base_apk_path,omitempty"`
	BaseVersion    string            `json:"base_version,omitempty"`
	BaseCode       int               `json:"base_code,omitempty"`
	Tools          []ToolStatus      `json:"tools"`
	BuildStatus    string            `json:"build_status"` // idle / building / success / failed
	BuildError     string            `json:"build_error,omitempty"`
	ApkURL         string            `json:"apk_url,omitempty"`
	ApkURLs        []string          `json:"apk_urls,omitempty"`
	AvailableBases []BaseVersionInfo `json:"available_bases,omitempty"` // 所有可用的官方底本列表
}

// CustomSettings 定制化设置
type CustomSettings struct {
	AppName               string `json:"app_name"`
	PackageName           string `json:"package_name"`
	VersionName           string `json:"version_name"`
	VersionCode           int    `json:"version_code"`
	DefaultServerURL      string `json:"default_server_url"`
	CustomKeystoreEnabled bool   `json:"custom_keystore_enabled"`
	KeystoreAlias         string `json:"keystore_alias"`
	KeystorePassword      string `json:"keystore_password"`
	KeyPassword           string `json:"key_password"`
	BaseVersion           string `json:"base_version"` // 选用的官方底本目录名，如 "116_v1.1.16"
}

type CustomService struct {
	db            *sql.DB
	toolsDir      string
	baseDir       string
	downloading   int32  // 0: idle, 1: downloading
	buildStatus   string // idle, building, success, failed
	buildError    string
	buildLog      strings.Builder
	buildLogMu    sync.Mutex
	progressMap   sync.Map // key: tool filename, value: int progress
	errorMap      sync.Map // key: tool filename, value: string
	cancelFunc    context.CancelFunc
	cancelMu      sync.Mutex
	settingsCache *CustomSettings
}

func NewCustomService(db *sql.DB) *CustomService {
	toolsDir := filepath.Join("library", "apk-tools", "v1")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		slog.Error("Failed to create tools directory", "path", toolsDir, "error", err)
	}
	return &CustomService{
		db:          db,
		toolsDir:    toolsDir,
		baseDir:     filepath.Join("library", "apk-tools"),
		buildStatus: "idle",
	}
}

// GetEnvStatus 检查环境并返回状态
func (s *CustomService) GetEnvStatus() EnvStatus {
	var tools []ToolStatus

	// 检查工具链包（包含 apktool.jar、apksigner.jar、JRE、zipalign，架构专用）
	toolchainName := s.getToolchainName()
	toolsReady := true

	// 检查核心文件是否完整
	coreFiles := []string{"apktool.jar", "apksigner.jar"}
	allCoreReady := true
	for _, name := range coreFiles {
		if !fileExists(filepath.Join(s.toolsDir, name)) {
			allCoreReady = false
			break
		}
	}

	// 检查 JRE
	jreBin := "java"
	zipalignBin := "zipalign"
	if runtime.GOOS == "windows" {
		jreBin = "java.exe"
		zipalignBin = "zipalign.exe"
	}
	jrePath := filepath.Join(s.toolsDir, "jre", "bin", jreBin)
	jreExists := fileExists(jrePath)
	if !jreExists {
		// 工具链内无 JRE 时，回退到系统 PATH
		if _, err := exec.LookPath(jreBin); err == nil {
			jreExists = true
		}
	}

	// 检查 zipalign
	nativeToolsPath := filepath.Join(s.toolsDir, zipalignBin)
	nativeToolsExists := fileExists(nativeToolsPath)
	if !nativeToolsExists {
		if _, err := exec.LookPath(zipalignBin); err == nil {
			nativeToolsExists = true
		}
	}

	// 工具链就绪 = 核心 jar + JRE + zipalign 都可用
	toolchainReady := allCoreReady && jreExists && nativeToolsExists
	if !toolchainReady {
		toolsReady = false
	}

	toolchainProg := 0
	if toolchainReady {
		toolchainProg = 100
	} else if v, ok := s.progressMap.Load(toolchainName); ok {
		toolchainProg = v.(int)
	}
	var toolchainErr string
	if val, ok := s.errorMap.Load(toolchainName); ok {
		toolchainErr = val.(string)
	}
	tools = append(tools, ToolStatus{Name: toolchainName, Exists: toolchainReady, Progress: toolchainProg, Error: toolchainErr})

	// 4. 检索选定或默认的官方底本 APK
	var baseApkPath string
	var baseVersion string
	var baseCode int
	var baseReady bool

	// 优先读取用户在定制页面里选择的目标底本
	var selectedBase string
	if settings, err := s.GetSettings(); err == nil {
		selectedBase = settings.BaseVersion
	}

	// 获取所有可供选择的官方底本列表
	availableBases := s.FindAvailableBaseApks()

	if selectedBase != "" {
		baseApkPath, baseVersion, baseCode, baseReady = s.FindBaseApkInDir(selectedBase)
	}

	// 如果选择的底本未就绪，或者未选择底本，则优先尝试使用列表中的最新大版本官方底本 (availableBases[0])
	if !baseReady && len(availableBases) > 0 {
		selectedBase = availableBases[0].Dir
		baseApkPath, baseVersion, baseCode, baseReady = s.FindBaseApkInDir(selectedBase)
	}

	// 最终兜底
	if !baseReady {
		baseApkPath, baseVersion, baseCode, baseReady = s.FindLatestBaseApk()
	}

	// 4. 读取最新的定制 APK 列表
	var apkURL string
	var apkURLs []string
	if s.buildStatus == "success" {
		if settings, err := s.GetSettings(); err == nil && settings.VersionCode > 0 {
			outDir := filepath.Join("web", "download", fmt.Sprintf("%d_%s", settings.VersionCode, settings.VersionName))
			if entries, err := os.ReadDir(outDir); err == nil {
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".apk") && strings.Contains(strings.ToLower(entry.Name()), "custom") {
						url := fmt.Sprintf("/download/%d_%s/%s", settings.VersionCode, settings.VersionName, entry.Name())
						apkURLs = append(apkURLs, url)
					}
				}
			}
		}
		if len(apkURLs) > 0 {
			apkURL = apkURLs[0] // 默认保留第一个链接作为兼容 fallback
		}
	}

	return EnvStatus{
		Arch:           runtime.GOARCH,
		ToolsReady:     toolsReady,
		Downloading:    atomic.LoadInt32(&s.downloading) == 1,
		BaseApkReady:   baseReady,
		BaseApkPath:    baseApkPath,
		BaseVersion:    baseVersion,
		BaseCode:       baseCode,
		Tools:          tools,
		BuildStatus:    s.buildStatus,
		BuildError:     s.buildError,
		ApkURL:         apkURL,
		ApkURLs:        apkURLs,
		AvailableBases: availableBases,
	}
}

// FindLatestBaseApk 检索 web/download 下最新版本的官方 APK 路径
func (s *CustomService) FindLatestBaseApk() (path string, versionName string, versionCode int, found bool) {
	downloadDir := filepath.Join("web", "download")
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return "", "", 0, false
	}

	maxCode := -1
	var bestDir string
	var bestVer string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 期待格式为 {versionCode}_{versionName}，例如 100_v1.0.0
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			continue
		}
		code, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		// 忽略定制版目录，防止套娃定制
		if strings.Contains(parts[1], "custom") {
			continue
		}

		if code > maxCode {
			maxCode = code
			bestDir = name
			bestVer = parts[1]
		}
	}

	if maxCode == -1 {
		return "", "", 0, false
	}

	// 扫描该文件夹下的任意 .apk 文件 (排除带有 custom 命名标志的)
	apkDir := filepath.Join(downloadDir, bestDir)
	apkEntries, err := os.ReadDir(apkDir)
	if err != nil {
		return "", "", 0, false
	}

	for _, apkEntry := range apkEntries {
		if apkEntry.IsDir() {
			continue
		}
		filename := apkEntry.Name()
		if strings.HasSuffix(strings.ToLower(filename), ".apk") && !strings.Contains(strings.ToLower(filename), "custom") {
			return filepath.Join(apkDir, filename), bestVer, maxCode, true
		}
	}

	return "", "", 0, false
}

// getToolchainName 返回当前平台+架构对应的工具链包名
func (s *CustomService) getToolchainName() string {
	if runtime.GOOS == "windows" {
		return "toolchain-windows-x64.tar.gz"
	}
	if runtime.GOOS == "darwin" {
		if runtime.GOARCH == "arm64" {
			return "toolchain-darwin-arm64.tar.gz"
		}
		return "toolchain-darwin-x64.tar.gz"
	}
	arch := runtime.GOARCH
	if arch == "arm64" {
		return "toolchain-linux-arm64.tar.gz"
	} else if strings.HasPrefix(arch, "arm") {
		return "toolchain-linux-arm.tar.gz"
	}
	return "toolchain-linux-x64.tar.gz"
}

// SetupEnvironment 触发下载架构专属的工具链包
func (s *CustomService) SetupEnvironment(proxyURL string) error {
	if !atomic.CompareAndSwapInt32(&s.downloading, 0, 1) {
		return errors.New("打包环境正在下载部署中，请勿重复触发")
	}

	// 清空历史错误
	s.errorMap.Range(func(key, value interface{}) bool {
		s.errorMap.Delete(key)
		return true
	})

	s.cancelMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelFunc = cancel
	s.cancelMu.Unlock()

	toolchainName := s.getToolchainName()

	// 重置下载进度映射表为 0
	s.progressMap.Store(toolchainName, 0)

	baseURL := "https://github.com/kuai410022283/mediaplayer/releases/download/app-tools/"

	finalURL := baseURL + toolchainName
	if proxyURL != "" {
		if !strings.HasSuffix(proxyURL, "/") {
			finalURL = proxyURL + "/" + finalURL
		} else {
			finalURL = proxyURL + finalURL
		}
	}

	go func() {
		defer func() {
			s.cancelMu.Lock()
			s.cancelFunc = nil
			s.cancelMu.Unlock()
			atomic.StoreInt32(&s.downloading, 0)
		}()

		err := s.downloadTool(ctx, finalURL, toolchainName)
		if err != nil {
			slog.Error("Failed to download toolchain", "name", toolchainName, "error", err)
			s.errorMap.Store(toolchainName, err.Error())
		} else {
			s.progressMap.Store(toolchainName, 100)
		}
	}()

	return nil
}

// CancelSetupEnvironment 强行终止打包部署下载任务
func (s *CustomService) CancelSetupEnvironment() {
	s.cancelMu.Lock()
	if s.cancelFunc != nil {
		s.cancelFunc()
		s.cancelFunc = nil
	}
	s.cancelMu.Unlock()

	// 将所有未完成的进度标记为\u201c下载已取消\u201d错误
	s.progressMap.Range(func(key, value interface{}) bool {
		filename := key.(string)
		prog := value.(int)
		if prog < 100 {
			s.errorMap.Store(filename, "\u4e0b\u8f7d\u5df2\u53d6\u6d88")
		}
		return true
	})
}

// ResetEnvironment 一键清理打包环境依赖（删除 library/apk-tools/v1 目录）
func (s *CustomService) ResetEnvironment() error {
	if err := os.RemoveAll(s.toolsDir); err != nil {
		return fmt.Errorf("清理依赖目录失败: %w", err)
	}
	// 重新创建空目录
	if err := os.MkdirAll(s.toolsDir, 0755); err != nil {
		return fmt.Errorf("重建依赖目录失败: %w", err)
	}
	// 清除下载进度
	s.progressMap.Range(func(key, value interface{}) bool {
		s.progressMap.Delete(key)
		return true
	})
	s.errorMap.Range(func(key, value interface{}) bool {
		s.errorMap.Delete(key)
		return true
	})
	return nil
}

// UploadedFileInfo 上传文件状态
type UploadedFileInfo struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Exists  bool   `json:"exists"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

// GetUploadedFileStatus 获取所有已上传文件的状态
func (s *CustomService) GetUploadedFileStatus() []UploadedFileInfo {
	files := []struct {
		ftype string
		path  string
		name  string
	}{
		{"jks", filepath.Join(s.toolsDir, "user-release-key.jks"), "user-release-key.jks"},
		{"logo", filepath.Join(s.toolsDir, "logo.png"), "logo.png"},
		{"banner", filepath.Join(s.toolsDir, "custom_banner.png"), "custom_banner.png"},
	}

	var result []UploadedFileInfo
	for _, f := range files {
		info := UploadedFileInfo{Type: f.ftype, Name: f.name}
		if stat, err := os.Stat(f.path); err == nil && !stat.IsDir() {
			info.Exists = true
			info.Size = stat.Size()
			info.ModTime = stat.ModTime().Format("2006-01-02 15:04:05")
		}
		result = append(result, info)
	}
	return result
}

// DeleteUploadedFile 删除指定类型的已上传文件
func (s *CustomService) DeleteUploadedFile(fileType string) error {
	var filePath string
	var key string
	switch fileType {
	case "jks":
		filePath = filepath.Join(s.toolsDir, "user-release-key.jks")
		key = "custom_jks_path"
	case "logo":
		filePath = filepath.Join(s.toolsDir, "logo.png")
		key = "custom_logo_path"
	case "banner":
		filePath = filepath.Join(s.toolsDir, "custom_banner.png")
		key = "custom_banner_path"
	default:
		return fmt.Errorf("不支持的文件类型: %s", fileType)
	}
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除文件失败: %w", err)
	}
	// 清除数据库中的路径记录
	if s.db != nil {
		_, _ = s.db.Exec(`DELETE FROM user_settings WHERE key=?`, key)
	}
	return nil
}

// DownloadVersionInfo 已下载的版本目录信息
type DownloadVersionInfo struct {
	Dir     string `json:"dir"`
	ModTime string `json:"mod_time"`
	HasApk  bool   `json:"has_apk"`
	Size    string `json:"size"`
}

// ListDownloadVersions 列出 web/download 下所有已下载的版本目录
func (s *CustomService) ListDownloadVersions() []DownloadVersionInfo {
	downloadDir := filepath.Join("web", "download")
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return nil
	}

	var list []DownloadVersionInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		dirPath := filepath.Join(downloadDir, entry.Name())
		hasApk := false
		var totalSize int64
		_ = filepath.Walk(dirPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if !fi.IsDir() {
				totalSize += fi.Size()
				if strings.HasSuffix(strings.ToLower(fi.Name()), ".apk") {
					hasApk = true
				}
			}
			return nil
		})

		sizeStr := formatSize(totalSize)
		modTime := info.ModTime().Format("2006-01-02 15:04:05")
		list = append(list, DownloadVersionInfo{
			Dir:     entry.Name(),
			ModTime: modTime,
			HasApk:  hasApk,
			Size:    sizeStr,
		})
	}
	return list
}

// DeleteDownloadVersion 删除指定版本目录（web/download/{dir}）
func (s *CustomService) DeleteDownloadVersion(dir string) error {
	// 防止路径穿越
	dir = filepath.Base(dir)
	if dir == "." || dir == ".." || dir == "" {
		return fmt.Errorf("无效的目录名")
	}
	dirPath := filepath.Join("web", "download", dir)
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return fmt.Errorf("目录不存在: %s", dir)
	}
	return os.RemoveAll(dirPath)
}

// formatSize 字节数转人性化大小
func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func (s *CustomService) downloadTool(ctx context.Context, url, filename string) error {
	destPath := filepath.Join(s.toolsDir, filename)

	// 检查核心文件是否已就绪（不依赖 tar.gz 包是否存在）
	if strings.HasPrefix(filename, "toolchain-") {
		jreBin := "java"
		zipalignBin := "zipalign"
		if runtime.GOOS == "windows" {
			jreBin = "java.exe"
			zipalignBin = "zipalign.exe"
		}
		if fileExists(filepath.Join(s.toolsDir, "apktool.jar")) &&
			fileExists(filepath.Join(s.toolsDir, "apksigner.jar")) &&
			fileExists(filepath.Join(s.toolsDir, "jre", "bin", jreBin)) &&
			fileExists(filepath.Join(s.toolsDir, zipalignBin)) {
			s.progressMap.Store(filename, 100)
			return nil
		}
	} else if fileExists(destPath) {
		// 非 tar.gz 文件已存在则跳过
		s.progressMap.Store(filename, 100)
		return nil
	}

	// 多源尝试：直连 GitHub URL 时优先通过 ghproxy 加速，已有代理则跳过避免双重代理
	mirrors := []string{url}
	if strings.HasPrefix(url, "https://github.com/") {
		mirrors = append([]string{"https://mirror.ghproxy.com/" + url}, mirrors...)
	}

	var resp *http.Response
	var err error
	for _, downloadURL := range mirrors {
		req, reqErr := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
		if reqErr != nil {
			err = reqErr
			continue
		}
		client := &http.Client{Timeout: 10 * time.Minute}
		resp, err = client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("下载状态异常: %d", resp.StatusCode)
	}
	defer resp.Body.Close()

	size := resp.ContentLength
	tempFile := destPath + ".tmp"
	if err := os.MkdirAll(filepath.Dir(tempFile), 0755); err != nil {
		return fmt.Errorf("创建下载目录失败: %w", err)
	}
	out, err := os.Create(tempFile)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		_ = os.Remove(tempFile)
	}()

	// 带进度的拷贝
	buffer := make([]byte, 32*1024)
	var downloaded int64
	for {
		n, readErr := resp.Body.Read(buffer)
		if n > 0 {
			_, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)
			if size > 0 {
				pct := int(float64(downloaded) / float64(size) * 100)
				s.progressMap.Store(filename, pct)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	_ = out.Close()

	// 下载完成，重命名文件
	if err := os.Rename(tempFile, destPath); err != nil {
		return err
	}

	// 如果是 tar.gz 压缩包，立即触发后台解包
	if strings.HasSuffix(filename, ".tar.gz") {
		if strings.HasPrefix(filename, "toolchain-") {
			// 架构专属工具链包：解压到 s.toolsDir 根目录
			s.progressMap.Store(filename, 95)

			// 解压前先清理旧文件（同时清理 Linux 和 Windows 两种格式的文件名）
			_ = os.RemoveAll(filepath.Join(s.toolsDir, "jre"))
			_ = os.Remove(filepath.Join(s.toolsDir, "apktool.jar"))
			_ = os.Remove(filepath.Join(s.toolsDir, "apksigner.jar"))
			_ = os.Remove(filepath.Join(s.toolsDir, "zipalign"))
			_ = os.Remove(filepath.Join(s.toolsDir, "zipalign.exe"))
			_ = os.Remove(filepath.Join(s.toolsDir, "aapt2"))
			_ = os.Remove(filepath.Join(s.toolsDir, "aapt2.exe"))
			err := s.extractTarGz(destPath, s.toolsDir)
			if err != nil {
				_ = os.Remove(destPath)
				return fmt.Errorf("解压工具链失败: %w", err)
			}
			// 赋予可执行权限
			_ = os.Chmod(filepath.Join(s.toolsDir, "jre", "bin", "java"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "jre", "bin", "java.exe"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "zipalign"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "zipalign.exe"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "aapt2"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "aapt2.exe"), 0755)
		} else {
			// 兼容旧版独立包下载（如 linux_tools.tar.gz、jre_linux_*.tar.gz）
			s.progressMap.Store(filename, 95)
			err := s.extractTarGz(destPath, s.toolsDir)
			if err != nil {
				_ = os.Remove(destPath)
				return fmt.Errorf("解压 %s 失败: %w", filename, err)
			}
			_ = os.Chmod(filepath.Join(s.toolsDir, "jre", "bin", "java"), 0755)
			_ = os.Chmod(filepath.Join(s.toolsDir, "zipalign"), 0755)
		}
		// 解压完成后删除 tar.gz 包，节省空间
		_ = os.Remove(destPath)
	}

	s.progressMap.Store(filename, 100)
	return nil
}

func (s *CustomService) extractTarGz(tarPath, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	file, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		// 净化路径，防止 zip-slip 漏洞
		cleaned := filepath.Clean(header.Name)
		if strings.Contains(cleaned, "..") || filepath.IsAbs(cleaned) {
			continue
		}

		// 如果 tar 包内有顶层目录，剥离一层（如 jre-17.0.20+8/ → jre/）
		// 如果 tar 包内文件直接平铺，则直接提取
		// tar 包内路径始终使用 "/" 分隔符，不受宿主机 OS 影响
		parts := strings.Split(cleaned, "/")
		targetRelative := cleaned
		if len(parts) > 1 && strings.HasPrefix(parts[0], "jre") && parts[0] != "jre" {
			// 顶层目录为 jre-xxx 变体时重命名为 jre
			targetRelative = filepath.Join(append([]string{"jre"}, parts[1:]...)...)
		}
		targetPath := filepath.Join(destDir, targetRelative)

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			// 确保 parent directory 存在
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			outFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, header.FileInfo().Mode())
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return err
			}
			_ = outFile.Close()
		}
	}
	return nil
}

// GetSettings 读取定制参数
func (s *CustomService) GetSettings() (CustomSettings, error) {
	if s.settingsCache != nil {
		return *s.settingsCache, nil
	}
	var settings CustomSettings
	// 从数据库系统配置表加载设置
	settings.AppName = s.getSettingDb("custom_app_name", "MediaPlayer")
	settings.VersionName = s.getSettingDb("custom_version_name", "1.0.0")
	vCodeStr := s.getSettingDb("custom_version_code", "100")
	settings.VersionCode, _ = strconv.Atoi(vCodeStr)
	settings.DefaultServerURL = s.getSettingDb("custom_default_server_url", "")
	settings.CustomKeystoreEnabled = s.getSettingDb("custom_keystore_enabled", "false") == "true"
	settings.KeystoreAlias = s.getSettingDb("custom_keystore_alias", "my-key-alias")
	settings.KeystorePassword = s.getSettingDb("custom_keystore_password", "123456")
	settings.KeyPassword = s.getSettingDb("custom_key_password", "123456")
	settings.BaseVersion = s.getSettingDb("custom_base_version", "")

	return settings, nil
}

// SaveSettings 保存定制参数
func (s *CustomService) SaveSettings(settings CustomSettings) error {
	s.settingsCache = &settings
	if s.db == nil {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	save := func(k, v string) {
		_, _ = tx.Exec(`INSERT INTO user_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
	}

	save("custom_app_name", settings.AppName)
	save("custom_version_name", settings.VersionName)
	save("custom_version_code", strconv.Itoa(settings.VersionCode))
	save("custom_default_server_url", settings.DefaultServerURL)
	save("custom_keystore_enabled", strconv.FormatBool(settings.CustomKeystoreEnabled))
	save("custom_keystore_alias", settings.KeystoreAlias)
	save("custom_keystore_password", settings.KeystorePassword)
	save("custom_key_password", settings.KeyPassword)
	save("custom_base_version", settings.BaseVersion)

	return tx.Commit()
}

func (s *CustomService) getSettingDb(key, fallback string) string {
	if s.db == nil {
		return fallback
	}
	var val string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key=?`, key).Scan(&val)
	if err != nil {
		return fallback
	}
	return val
}

// SaveUserJks 保存用户上传的 JKS 证书文件
func (s *CustomService) SaveUserJks(r io.Reader) error {
	dest := filepath.Join(s.toolsDir, "user-release-key.jks")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建证书目录失败: %w", err)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, r)
	return err
}

// SaveUserLogo 保存用户上传的 PNG 图标并记录配置
func (s *CustomService) SaveUserLogo(r io.Reader) error {
	dest := filepath.Join(s.toolsDir, "logo.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建图标目录失败: %w", err)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO user_settings (key, value) VALUES ('custom_logo_path', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, dest)
	return err
}

// SaveUserBanner 保存用户上传的 TV 宽屏横幅并记录配置
func (s *CustomService) SaveUserBanner(r io.Reader) error {
	dest := filepath.Join(s.toolsDir, "custom_banner.png")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return fmt.Errorf("创建横幅目录失败: %w", err)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, r)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO user_settings (key, value) VALUES ('custom_banner_path', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, dest)
	return err
}

// StartBuildPackage 启动异步编译定制客户端
func (s *CustomService) StartBuildPackage() error {
	env := s.GetEnvStatus()
	if !env.ToolsReady {
		return errors.New("编译工具链尚未准备就绪，无法编译")
	}
	if !env.BaseApkReady {
		return errors.New("未检测到官方基础版本包底本，请先通过系统升级获取")
	}
	if s.buildStatus == "building" {
		return errors.New("编译任务正在进行中，请勿重复发起")
	}

	s.buildStatus = "building"
	s.buildError = ""
	s.buildLogMu.Lock()
	s.buildLog.Reset()
	s.buildLogMu.Unlock()

	go s.runBuild(env.BaseApkPath)
	return nil
}

func (s *CustomService) appendLog(format string, args ...interface{}) {
	s.buildLogMu.Lock()
	defer s.buildLogMu.Unlock()
	s.buildLog.WriteString(fmt.Sprintf(format+"\n", args...))
}

func (s *CustomService) GetBuildLog() string {
	s.buildLogMu.Lock()
	defer s.buildLogMu.Unlock()
	return s.buildLog.String()
}

//go:embed default-signing.p12
var embeddedDefaultKeystore []byte

func (s *CustomService) runBuild(baseApkPath string) {
	// 1. 崩溃恢复与兜底
	defer func() {
		if r := recover(); r != nil {
			slog.Error("APK定制构建发生严重异常崩溃", "err", r, "stack", string(debug.Stack()))
			s.buildStatus = "failed"
			s.buildError = fmt.Sprintf("系统内部崩溃异常: %v", r)
			s.appendLog("[FATAL] 构建进程崩溃已拦截恢复: %v", r)
		}
	}()

	s.appendLog(">>> 开始执行 Go 原生 APK 批量定制构建流程...")
	settings, err := s.GetSettings()
	if err != nil {
		s.buildStatus = "failed"
		s.buildError = "加载配置失败: " + err.Error()
		s.appendLog("[ERROR] %s", s.buildError)
		return
	}

	// 确认输出存放的目录是否存在
	outputDir := filepath.Join("web", "download", fmt.Sprintf("%d_%s", settings.VersionCode, settings.VersionName))
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		s.buildStatus = "failed"
		s.buildError = "无法创建输出目录: " + err.Error()
		s.appendLog("[ERROR] %s", s.buildError)
		return
	}

	// 扫描底本目录下的所有 .apk 文件进行批量定制
	baseDir := filepath.Dir(baseApkPath)
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		s.buildStatus = "failed"
		s.buildError = "读取底本目录失败: " + err.Error()
		s.appendLog("[ERROR] %s", s.buildError)
		return
	}

	var baseApks []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".apk") && !strings.Contains(strings.ToLower(entry.Name()), "custom") {
			baseApks = append(baseApks, filepath.Join(baseDir, entry.Name()))
		}
	}

	if len(baseApks) == 0 {
		s.buildStatus = "failed"
		s.buildError = "未在底本目录找到有效的 APK 底本"
		s.appendLog("[ERROR] %s", s.buildError)
		return
	}

	s.appendLog("[INFO] 检测到待定制 APK 底本共 %d 个:", len(baseApks))
	for _, apk := range baseApks {
		s.appendLog("  - %s", filepath.Base(apk))
	}

	// 2. 检测并准备 Java 可执行文件路径
	// 优先使用工具链 JRE（与 aapt2/zipalign 同源，确保全平台兼容性一致），
	// 工具链不可用时回退到系统 Java
	javaCmd := "java"
	localJava := filepath.Join(s.toolsDir, "jre", "bin", "java")
	if fileExists(localJava) {
		_ = os.Chmod(localJava, 0755)
		javaCmd = localJava
		slog.Info("使用内置本地 JRE", "path", javaCmd)
	} else if systemJava, err := exec.LookPath("java"); err == nil {
		javaCmd = systemJava
		s.appendLog("[INFO] 使用系统 Java: %s", javaCmd)
	} else {
		s.appendLog("[WARN] 未找到可用的 Java 运行时，请确保系统已安装 Java 或下载 JRE 工具包")
	}

	apktoolJar := filepath.Join(s.toolsDir, "apktool.jar")
	apksignerJar := filepath.Join(s.toolsDir, "apksigner.jar")

	if !fileExists(apktoolJar) || !fileExists(apksignerJar) {
		s.buildStatus = "failed"
		s.buildError = "构建核心编译工具包 (apktool.jar / apksigner.jar) 缺失，请在系统环境设置中下载"
		s.appendLog("[ERROR] %s", s.buildError)
		return
	}

	// 校验并准备签名证书（优先级：自定义证书 > 内嵌默认证书）
	keystorePath := filepath.Join(s.toolsDir, "default-signing.p12")
	alias := "mediaplayer-key"
	storePass := "123456"
	keyPass := "123456"

	if settings.CustomKeystoreEnabled {
		userJks := filepath.Join(s.toolsDir, "user-release-key.jks")
		if fileExists(userJks) {
			keystorePath = userJks
			alias = settings.KeystoreAlias
			storePass = settings.KeystorePassword
			keyPass = settings.KeyPassword
			s.appendLog("[INFO] 正在启用自定义签名证书: %s", keystorePath)
		} else {
			s.appendLog("[WARN] 自定义签名证书不存在，自动退化使用默认签名！")
		}
	}

	// 未使用自定义证书时，释放内嵌的默认签名证书（PKCS12 格式，apksigner 原生支持）
	if keystorePath == filepath.Join(s.toolsDir, "default-signing.p12") {
		if err := os.MkdirAll(filepath.Dir(keystorePath), 0755); err == nil {
			if err := os.WriteFile(keystorePath, embeddedDefaultKeystore, 0644); err != nil {
				s.buildStatus = "failed"
				s.buildError = "释放默认签名证书失败: " + err.Error()
				s.appendLog("[ERROR] %s", s.buildError)
				return
			}
		}
		slog.Info("已启用内嵌默认签名证书")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// 依次处理所有的底本 APK
	for idx, currentBaseApk := range baseApks {
		baseName := filepath.Base(currentBaseApk)
		s.appendLog("\n>>> ------------------------------------------------------------")
		s.appendLog(">>> 正在生成定制客户端 (%d/%d): %s", idx+1, len(baseApks), baseName)
		s.appendLog(">>> ------------------------------------------------------------")

		// 动态生成输出文件名
		outApkName := strings.TrimSuffix(baseName, filepath.Ext(baseName)) + "_custom.apk"
		outputApkPath := filepath.Join(outputDir, outApkName)

		// 为当前 APK 创建单独的隔离临时文件夹
		tempDir, err := os.MkdirTemp("", fmt.Sprintf("apk_cust_%d_*", idx))
		if err != nil {
			s.buildStatus = "failed"
			s.buildError = fmt.Sprintf("[%s] 无法创建临时工作目录: %v", baseName, err)
			s.appendLog("[ERROR] %s", s.buildError)
			return
		}

		// 任务执行完成后彻底清空此临时文件夹
		func() {
			defer func() { _ = os.RemoveAll(tempDir) }()

			tempUnsignedApk := filepath.Join(tempDir, "temp_unsigned.apk")
			tempAlignedApk := filepath.Join(tempDir, "temp_aligned.apk")
			tempUnpackedDir := filepath.Join(tempDir, "unpacked")
			tempFrameDir := filepath.Join(tempDir, "framework")
			_ = os.MkdirAll(tempFrameDir, 0755)

			// 尝试预先创建用户主目录下的 apktool 框架目录，防止权限不足报错
			if userHome, err := os.UserHomeDir(); err == nil && userHome != "" {
				_ = os.MkdirAll(filepath.Join(userHome, ".local", "share", "apktool", "framework"), 0755)
			}

			// 0. 安装框架文件，确保不同环境（虚拟机/实体机）资源解码一致
			s.appendLog(">>> [1/6] 正在安装框架资源...")
			installFrameArgs := []string{"-jar", apktoolJar, "if", "-p", tempFrameDir, currentBaseApk}
			if err := ExecuteCommandWithLog(ctx, javaCmd, installFrameArgs, s.appendLog); err != nil {
				s.appendLog("[WARN] 安装框架资源失败，尝试继续: %v", err)
				// 不中断流程，某些 APK 可能不需要框架文件
			}

			// 1. 反编译
			s.appendLog(">>> [2/6] 正在解析应用底本...")
			decompileArgs := []string{"-jar", apktoolJar, "d", "-p", tempFrameDir, currentBaseApk, "-o", tempUnpackedDir, "-f"}
			if err := ExecuteCommandWithLog(ctx, javaCmd, decompileArgs, s.appendLog); err != nil {
				s.buildStatus = "failed"
				s.buildError = fmt.Sprintf("[%s] 解析应用底本失败: %v", baseName, err)
				return
			}

			// 2. 定制修改
			modifiers := []ApkModifier{
				&AppNameModifier{},
				&PackageNameModifier{},
				&VersionModifier{},
				&LogoAndBannerModifier{
					CustomLogoPath:   s.getSettingDb("custom_logo_path", ""),
					CustomBannerPath: s.getSettingDb("custom_banner_path", ""),
				},
				&SmaliConfigModifier{},
			}
			s.appendLog(">>> [3/6] 正在应用个性化定制配置...")
			for _, mod := range modifiers {
				if err := mod.Modify(ctx, tempUnpackedDir, &settings, s.appendLog); err != nil {
					s.buildStatus = "failed"
					s.buildError = fmt.Sprintf("[%s] 配置项 %s 处理失败: %v", baseName, mod.Name(), err)
					return
				}
			}

			// 清理 public.xml 中 $ 开头的非法资源名（aapt2 不支持）
			publicXmlPath := filepath.Join(tempUnpackedDir, "res", "values", "public.xml")
			if data, err := os.ReadFile(publicXmlPath); err == nil {
				re := regexp.MustCompile(`(?m)^\s*<public[^>]*name="\$[^"]*"[^>]*/>\s*$`)
				cleaned := re.ReplaceAll(data, nil)
				if len(cleaned) != len(data) {
					if err := os.WriteFile(publicXmlPath, cleaned, 0644); err == nil {
						slog.Info("已清理 public.xml 中不合法的资源条目", "path", publicXmlPath)
					}
				}
			}

			// 3. 重新编译打包
			s.appendLog(">>> [4/6] 正在编译构建客户端应用...")
			// apktool 3.0.x 仅支持 aapt2，优先使用工具链中的 aapt2，其次系统 aapt2
			aaptBin := ""
			aaptName := "aapt2"
			if runtime.GOOS == "windows" {
				aaptName = "aapt2.exe"
			}
			if fileExists(filepath.Join(s.toolsDir, aaptName)) {
				aaptBin = filepath.Join(s.toolsDir, aaptName)
				slog.Info("使用工具链原生 aapt2", "path", aaptBin)
			} else if runtime.GOOS == "windows" && fileExists(filepath.Join(s.toolsDir, "aapt2")) {
				// Windows 上也尝试检查无后缀版本（兼容旧工具链）
				aaptBin = filepath.Join(s.toolsDir, "aapt2")
				slog.Info("使用工具链原生 aapt2", "path", aaptBin)
			} else if fileExists("/usr/bin/aapt2") {
				aaptBin = "/usr/bin/aapt2"
			} else if p, err := exec.LookPath(aaptName); err == nil {
				aaptBin = p
			}
			// 注意：工具链 JRE 的 argument parser 会拦截 -a/-c/-nc 等短选项，
			// 因此使用 --aapt 长格式，避免被 JRE 误解析为 Java 选项
			rebuildArgs := []string{"-jar", apktoolJar, "b", "-p", tempFrameDir, tempUnpackedDir, "-o", tempUnsignedApk}
			if aaptBin != "" {
				rebuildArgs = append(rebuildArgs, "--aapt", aaptBin)
			} else {
				rebuildArgs = append(rebuildArgs, "--use-aapt2")
				slog.Info("使用 apktool 内置 aapt2")
			}
			if err := ExecuteCommandWithLog(ctx, javaCmd, rebuildArgs, s.appendLog); err != nil {
				s.buildStatus = "failed"
				s.buildError = fmt.Sprintf("[%s] 编译客户端失败: %v", baseName, err)
				return
			}

			// 4. 对齐优化
			s.appendLog(">>> [5/6] 正在优化应用结构...")
			execCmd, flags := s.getZipalignCmd(javaCmd, s.appendLog)
			aligned := false
			if execCmd != javaCmd {
				alignArgs := append(flags, tempUnsignedApk, tempAlignedApk)
				if err := ExecuteCommandWithLog(ctx, execCmd, alignArgs, s.appendLog); err != nil {
					slog.Warn("工具链 zipalign 执行失败，回退到原生对齐", "error", err)
				} else {
					aligned = true
				}
			}
			if !aligned {
				if err := NativeZipAlign(tempUnsignedApk, tempAlignedApk, 4); err != nil {
					s.buildStatus = "failed"
					s.buildError = fmt.Sprintf("[%s] 原生对齐优化失败: %v", baseName, err)
					return
				}
			}

			// 5. 签名
			s.appendLog(">>> [6/6] 正在完成数字签名...")
			signArgs := []string{
				"-jar", apksignerJar, "sign",
				"--ks", keystorePath,
				"--ks-key-alias", alias,
				"--ks-pass", "pass:" + storePass,
				"--key-pass", "pass:" + keyPass,
				"--v1-signing-enabled", "true",
				"--v2-signing-enabled", "true",
				"--out", outputApkPath,
				tempAlignedApk,
			}
			if err := ExecuteCommandWithLog(ctx, javaCmd, signArgs, s.appendLog); err != nil {
				s.buildStatus = "failed"
				s.buildError = fmt.Sprintf("[%s] 数字签名失败: %v", baseName, err)
				return
			}

			s.appendLog("[SUCCESS] 成功生成定制客户端: %s", outApkName)
		}()

		// 如果在中途某一个底本打包出错，立即中止后续打包
		if s.buildStatus == "failed" {
			s.appendLog("[ERROR] 打包中断。")
			return
		}
	}

	// 11. 批量打包全部成功后，写入版本文件
	verTxtPath := filepath.Join(outputDir, "version.txt")
	_ = os.WriteFile(verTxtPath, []byte(fmt.Sprintf("%d\n%s\n", settings.VersionCode, settings.VersionName)), 0644)

	s.buildStatus = "success"
	s.appendLog("\n>>> ============================================================")
	s.appendLog(">>> 🎉 所有定制版 APK 批量成功打包完成！")
	s.appendLog(">>> OTA 升级配置文件已自动生成就绪。")
	s.appendLog(">>> ============================================================")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && info.Size() > 0
}

// FindAvailableBaseApks 检索 web/download 下所有包含官方基础 APK 的版本目录
func (s *CustomService) FindAvailableBaseApks() []BaseVersionInfo {
	var list []BaseVersionInfo
	downloadDir := filepath.Join("web", "download")
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		return list
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			continue
		}
		code, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		// 忽略带有 custom 关键字的目录，防止套娃定制
		if strings.Contains(parts[1], "custom") {
			continue
		}

		// 检查该目录下是否存在非 custom 的 .apk 文件
		apkDir := filepath.Join(downloadDir, name)
		apkEntries, err := os.ReadDir(apkDir)
		if err != nil {
			continue
		}
		hasBaseApk := false
		for _, apkEntry := range apkEntries {
			if !apkEntry.IsDir() && strings.HasSuffix(strings.ToLower(apkEntry.Name()), ".apk") && !strings.Contains(strings.ToLower(apkEntry.Name()), "custom") {
				hasBaseApk = true
				break
			}
		}
		if hasBaseApk {
			list = append(list, BaseVersionInfo{
				Version: parts[1],
				Code:    code,
				Dir:     name,
			})
		}
	}

	// 按照版本代码从大到小排序 (降序)，最新版本在前
	sort.Slice(list, func(i, j int) bool {
		return list[i].Code > list[j].Code
	})

	return list
}

// FindBaseApkInDir 获取特定底本文件夹下首个可用的官方 APK 路径
func (s *CustomService) FindBaseApkInDir(dirName string) (path string, versionName string, versionCode int, found bool) {
	parts := strings.SplitN(dirName, "_", 2)
	if len(parts) != 2 {
		return "", "", 0, false
	}
	code, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", "", 0, false
	}
	ver := parts[1]

	downloadDir := filepath.Join("web", "download")
	apkDir := filepath.Join(downloadDir, dirName)
	apkEntries, err := os.ReadDir(apkDir)
	if err != nil {
		return "", "", 0, false
	}

	for _, apkEntry := range apkEntries {
		if apkEntry.IsDir() {
			continue
		}
		filename := apkEntry.Name()
		if strings.HasSuffix(strings.ToLower(filename), ".apk") && !strings.Contains(strings.ToLower(filename), "custom") {
			return filepath.Join(apkDir, filename), ver, code, true
		}
	}
	return "", "", 0, false
}

// getZipalignCmd 智能检测并优先返回原生 zipalign 可执行二进制文件及参数
func (s *CustomService) getZipalignCmd(javaCmd string, logFunc func(string, ...interface{})) (cmd string, args []string) {
	// 1. 检测工具链中的 zipalign（根据平台检查 .exe 后缀）
	localBinName := "zipalign"
	if runtime.GOOS == "windows" {
		localBinName = "zipalign.exe"
	}
	localBin := filepath.Join(s.toolsDir, localBinName)
	if !fileExists(localBin) && runtime.GOOS == "windows" {
		// Windows 上也尝试检查无后缀版本（兼容旧工具链）
		localBin = filepath.Join(s.toolsDir, "zipalign")
	}
	if fileExists(localBin) {
		_ = os.Chmod(localBin, 0755)
		slog.Info("使用工具链原生 zipalign", "path", localBin)
		return localBin, []string{"-p", "-f", "4"}
	}

	binName := "zipalign"
	if runtime.GOOS == "windows" {
		binName = "zipalign.exe"
	}

	// 2. 检测 Android SDK build-tools
	home, _ := os.UserHomeDir()
	sdkPaths := []string{
		filepath.Join(home, "Android", "Sdk", "build-tools"),
		filepath.Join(home, "AppData", "Local", "Android", "Sdk", "build-tools"),
		"/usr/lib/android-sdk/build-tools",
	}
	for _, sdkDir := range sdkPaths {
		if entries, err := os.ReadDir(sdkDir); err == nil {
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i].IsDir() {
					binPath := filepath.Join(sdkDir, entries[i].Name(), binName)
					if fileExists(binPath) {
						slog.Info("检测到 Android SDK zipalign", "path", binPath)
						return binPath, []string{"-p", "-f", "4"}
					}
				}
			}
		}
	}

	// 3. 检测系统环境变量 PATH 中的 zipalign
	if path, err := exec.LookPath(binName); err == nil {
		slog.Info("使用系统环境变量中的 zipalign", "path", path)
		return path, []string{"-p", "-f", "4"}
	}

	// 4. 退化到纯 Go 原生对齐
	return javaCmd, nil
}

// NativeZipAlign 纯 Go 语言实现的 Android 0xd990 规范 4 字节 Zip 边界对齐算法
// 手动写入 ZIP 结构，避免 zip.Writer 内部缓冲导致偏移量计算错误
func NativeZipAlign(inputPath, outputPath string, align int64) error {
	r, err := zip.OpenReader(inputPath)
	if err != nil {
		return fmt.Errorf("读取 Zip 失败: %w", err)
	}
	defer func() { _ = r.Close() }()

	out, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("创建输出 Zip 失败: %w", err)
	}
	defer func() { _ = out.Close() }()

	// 手动写入，精确跟踪偏移量
	type cdEntry struct {
		header []byte // 预构建的 central directory entry
	}
	var cdEntries []cdEntry
	var offset int64
	buf := make([]byte, 0, 64*1024)

	writeBuf := func(data []byte) error {
		n, err := out.Write(data)
		offset += int64(n)
		return err
	}
	writeBufFull := func(data []byte) error {
		for len(data) > 0 {
			n, err := out.Write(data)
			if err != nil {
				return err
			}
			offset += int64(n)
			data = data[n:]
		}
		return nil
	}

	for _, f := range r.File {
		header := f.FileHeader
		cleanExtra := cleanAlignmentExtra(header.Extra)

		// 计算数据偏移量，添加对齐填充
		extra := cleanExtra
		if header.Method == zip.Store {
			baseDataOffset := offset + 30 + int64(len(header.Name)) + int64(len(extra))
			padding := (align - (baseDataOffset % align)) % align
			if padding > 0 {
				if padding < 4 {
					padding += align
				}
				extraPadding := make([]byte, padding)
				binary.LittleEndian.PutUint16(extraPadding[0:2], 0xd990)
				binary.LittleEndian.PutUint16(extraPadding[2:4], uint16(padding-4))
				extra = append(extra, extraPadding...)
			}
		}

		localHeaderOffset := offset

		// 构建 local file header
		buf = buf[:0]
		buf = binary.LittleEndian.AppendUint32(buf, 0x04034b50)           // signature
		buf = binary.LittleEndian.AppendUint16(buf, header.ReaderVersion) // version needed
		buf = binary.LittleEndian.AppendUint16(buf, header.Flags)         // flags
		buf = binary.LittleEndian.AppendUint16(buf, header.Method)        // method
		modifiedTime, modifiedDate := msDosTime(header.Modified)
		buf = binary.LittleEndian.AppendUint16(buf, modifiedTime) // mod time
		buf = binary.LittleEndian.AppendUint16(buf, modifiedDate) // mod date
		if header.Flags&0x0008 != 0 {
			buf = binary.LittleEndian.AppendUint32(buf, 0) // crc32 (in data descriptor)
			buf = binary.LittleEndian.AppendUint32(buf, 0) // compressed size
			buf = binary.LittleEndian.AppendUint32(buf, 0) // uncompressed size
		} else {
			buf = binary.LittleEndian.AppendUint32(buf, header.CRC32)
			buf = binary.LittleEndian.AppendUint32(buf, uint32(header.CompressedSize64))
			buf = binary.LittleEndian.AppendUint32(buf, uint32(header.UncompressedSize64))
		}
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(header.Name))) // name len
		buf = binary.LittleEndian.AppendUint16(buf, uint16(len(extra)))       // extra len
		buf = append(buf, header.Name...)
		buf = append(buf, extra...)
		if err := writeBuf(buf); err != nil {
			return fmt.Errorf("写入 local header 失败 (%s): %w", header.Name, err)
		}

		// 写入文件数据
		rc, err := f.OpenRaw()
		if err != nil {
			return fmt.Errorf("读取源 zip entry 失败 (%s): %w", header.Name, err)
		}
		dataSize := int64(header.CompressedSize64)
		written, err := io.Copy(out, rc)
		if closer, ok := rc.(io.Closer); ok {
			_ = closer.Close()
		}
		if err != nil {
			return fmt.Errorf("写入数据失败 (%s): %w", header.Name, err)
		}
		offset += written

		// 写入 data descriptor（如果 flags bit 3 设置）
		if header.Flags&0x0008 != 0 {
			dd := make([]byte, 16)
			binary.LittleEndian.PutUint32(dd[0:4], 0x08074b50) // data descriptor signature
			binary.LittleEndian.PutUint32(dd[4:8], header.CRC32)
			binary.LittleEndian.PutUint32(dd[8:12], uint32(header.CompressedSize64))
			binary.LittleEndian.PutUint32(dd[12:16], uint32(header.UncompressedSize64))
			if err := writeBuf(dd); err != nil {
				return fmt.Errorf("写入 data descriptor 失败 (%s): %w", header.Name, err)
			}
		}

		// 验证数据大小
		if written != dataSize {
			return fmt.Errorf("数据大小不匹配 (%s): 期望 %d, 实际 %d", header.Name, dataSize, written)
		}

		// 构建 central directory entry
		cd := make([]byte, 0, 46+len(header.Name)+len(extra)+len(header.Comment))
		cd = binary.LittleEndian.AppendUint32(cd, 0x02014b50)            // signature
		cd = binary.LittleEndian.AppendUint16(cd, header.CreatorVersion) // version made by
		cd = binary.LittleEndian.AppendUint16(cd, header.ReaderVersion)  // version needed
		cd = binary.LittleEndian.AppendUint16(cd, header.Flags)          // flags
		cd = binary.LittleEndian.AppendUint16(cd, header.Method)         // method
		cd = binary.LittleEndian.AppendUint16(cd, modifiedTime)          // mod time
		cd = binary.LittleEndian.AppendUint16(cd, modifiedDate)          // mod date
		cd = binary.LittleEndian.AppendUint32(cd, header.CRC32)
		cd = binary.LittleEndian.AppendUint32(cd, uint32(header.CompressedSize64))
		cd = binary.LittleEndian.AppendUint32(cd, uint32(header.UncompressedSize64))
		cd = binary.LittleEndian.AppendUint16(cd, uint16(len(header.Name)))    // name len
		cd = binary.LittleEndian.AppendUint16(cd, uint16(len(extra)))          // extra len
		cd = binary.LittleEndian.AppendUint16(cd, uint16(len(header.Comment))) // comment len
		cd = binary.LittleEndian.AppendUint16(cd, 0)                           // disk number start
		cd = binary.LittleEndian.AppendUint16(cd, 0)                           // internal attrs
		cd = binary.LittleEndian.AppendUint32(cd, 0)                           // external attrs
		cd = binary.LittleEndian.AppendUint32(cd, uint32(localHeaderOffset))   // local header offset
		cd = append(cd, header.Name...)
		cd = append(cd, extra...)
		cd = append(cd, header.Comment...)
		cdEntries = append(cdEntries, cdEntry{header: cd})
	}

	// 写入 central directory
	cdOffset := offset
	cdSize := int64(0)
	for _, e := range cdEntries {
		if err := writeBufFull(e.header); err != nil {
			return fmt.Errorf("写入 central directory 失败: %w", err)
		}
		cdSize += int64(len(e.header))
	}

	// 写入 end of central directory record
	eocd := make([]byte, 22)
	binary.LittleEndian.PutUint32(eocd[0:4], 0x06054b50)               // signature
	binary.LittleEndian.PutUint16(eocd[4:6], 0)                        // disk number
	binary.LittleEndian.PutUint16(eocd[6:8], 0)                        // cd disk number
	binary.LittleEndian.PutUint16(eocd[8:10], uint16(len(cdEntries)))  // cd entries on disk
	binary.LittleEndian.PutUint16(eocd[10:12], uint16(len(cdEntries))) // total cd entries
	binary.LittleEndian.PutUint32(eocd[12:16], uint32(cdSize))         // cd size
	binary.LittleEndian.PutUint32(eocd[16:20], uint32(cdOffset))       // cd offset
	binary.LittleEndian.PutUint16(eocd[20:22], 0)                      // comment length
	if err := writeBuf(eocd); err != nil {
		return fmt.Errorf("写入 EOCD 失败: %w", err)
	}

	return nil
}

// msDosTime 将 time.Time 转换为 MS-DOS 日期时间格式
func msDosTime(t time.Time) (timeVal, dateVal uint16) {
	timeVal = uint16(t.Second()/2 | t.Minute()<<5 | t.Hour()<<11)
	dateVal = uint16(t.Day() | int(t.Month())<<5 | (t.Year()-1980)<<9)
	return
}

func cleanAlignmentExtra(extra []byte) []byte {
	var result []byte
	i := 0
	for i+4 <= len(extra) {
		tag := binary.LittleEndian.Uint16(extra[i : i+2])
		size := int(binary.LittleEndian.Uint16(extra[i+2 : i+4]))
		if tag == 0xd990 {
			i += 4 + size
			continue
		}
		if i+4+size <= len(extra) {
			result = append(result, extra[i:i+4+size]...)
			i += 4 + size
		} else {
			break
		}
	}
	return result
}
