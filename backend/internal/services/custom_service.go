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
	Arch           string            `json:"arch"`           // amd64 / arm64
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
	arch := runtime.GOARCH
	var tools []ToolStatus

	// 1. 检查核心 jar 包
	jarFiles := []string{"apktool.jar", "apksigner.jar"}
	toolsReady := true

	for _, name := range jarFiles {
		path := filepath.Join(s.toolsDir, name)
		exists := fileExists(path)
		if !exists {
			toolsReady = false
		}
		prog := 0
		if exists {
			prog = 100
		} else if v, ok := s.progressMap.Load(name); ok {
			prog = v.(int)
		}
		var errMsg string
		if val, ok := s.errorMap.Load(name); ok {
			errMsg = val.(string)
		}
		tools = append(tools, ToolStatus{Name: name, Exists: exists, Progress: prog, Error: errMsg})
	}

	// 2. 检查 JRE 目录
	jreName := "jre_linux_x64.tar.gz"
	jreDirName := "jre-x64"
	if arch == "arm64" {
		jreName = "jre_linux_arm64.tar.gz"
		jreDirName = "jre-arm64"
	}
	jrePath := filepath.Join(s.toolsDir, jreDirName, "bin", "java")
	if runtime.GOOS == "windows" {
		jrePath = filepath.Join(s.toolsDir, jreDirName, "bin", "java.exe")
	}
	jreExists := fileExists(jrePath)
	if !jreExists {
		// 检查系统全局环境变量中是否存在 java 可执行命令
		if _, err := exec.LookPath("java"); err == nil {
			jreExists = true
		} else if _, err := exec.LookPath("java.exe"); err == nil {
			jreExists = true
		} else {
			toolsReady = false
		}
	}
	jreProg := 0
	if jreExists {
		jreProg = 100
	} else if v, ok := s.progressMap.Load(jreName); ok {
		jreProg = v.(int)
	}
	var jreErr string
	if val, ok := s.errorMap.Load(jreName); ok {
		jreErr = val.(string)
	}
	tools = append(tools, ToolStatus{Name: jreName, Exists: jreExists, Progress: jreProg, Error: jreErr})

	// 3. 检查 linux_tools 原生对齐工具链
	nativeToolsName := "linux_tools.tar.gz"
	binName := "zipalign"
	if runtime.GOOS == "windows" {
		binName = "zipalign.exe"
	} else if arch == "arm64" {
		binName = "zipalign_arm64"
	}
	nativeToolsPath := filepath.Join(s.toolsDir, "linux_tools", binName)
	nativeToolsExists := fileExists(nativeToolsPath)
	if !nativeToolsExists {
		// 检查系统全局环境变量中是否存在 zipalign
		if _, err := exec.LookPath("zipalign"); err == nil {
			nativeToolsExists = true
		} else if _, err := exec.LookPath("zipalign.exe"); err == nil {
			nativeToolsExists = true
		}
	}
	nativeToolsProg := 0
	if nativeToolsExists {
		nativeToolsProg = 100
	} else if v, ok := s.progressMap.Load(nativeToolsName); ok {
		nativeToolsProg = v.(int)
	}
	var nativeToolsErr string
	if val, ok := s.errorMap.Load(nativeToolsName); ok {
		nativeToolsErr = val.(string)
	}
	tools = append(tools, ToolStatus{Name: nativeToolsName, Exists: nativeToolsExists, Progress: nativeToolsProg, Error: nativeToolsErr})

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
		Arch:           arch,
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

// SetupEnvironment 触发并发下载 4 个依赖工具包
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

	arch := runtime.GOARCH
	var wg sync.WaitGroup

	// 需要下载的列表
	targets := []string{"apktool.jar", "apksigner.jar", "linux_tools.tar.gz"}
	jreName := "jre_linux_x64.tar.gz"
	if arch == "arm64" {
		jreName = "jre_linux_arm64.tar.gz"
	}
	targets = append(targets, jreName)

	// 重置下载进度映射表为 0
	for _, name := range targets {
		s.progressMap.Store(name, 0)
	}

	baseURL := "https://github.com/kuai410022283/mediaplayer/releases/download/app-tools/"

	for _, name := range targets {
		wg.Add(1)
		go func(filename string) {
			defer wg.Done()

			finalURL := baseURL + filename
			if proxyURL != "" {
				if !strings.HasSuffix(proxyURL, "/") {
					finalURL = proxyURL + "/" + finalURL
				} else {
					finalURL = proxyURL + finalURL
				}
			}

			err := s.downloadTool(ctx, finalURL, filename)
			if err != nil {
				slog.Error("Failed to download tool", "name", filename, "error", err)
				s.errorMap.Store(filename, err.Error())
			} else {
				s.progressMap.Store(filename, 100)
			}
		}(name)
	}

	go func() {
		wg.Wait()
		s.cancelMu.Lock()
		s.cancelFunc = nil
		s.cancelMu.Unlock()
		atomic.StoreInt32(&s.downloading, 0)
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
	arch := runtime.GOARCH
	destPath := filepath.Join(s.toolsDir, filename)
	// 判断文件是否已就绪
	if fileExists(destPath) {
		if !strings.HasSuffix(filename, ".tar.gz") {
			s.progressMap.Store(filename, 100)
			return nil
		}
		// 针对 tar.gz 检查其解压目录是否完好
		if filename == "linux_tools.tar.gz" {
			binName := "zipalign"
			if runtime.GOOS == "windows" {
				binName = "zipalign.exe"
			} else if arch == "arm64" {
				binName = "zipalign_arm64"
			}
			destDir := filepath.Join(s.toolsDir, "linux_tools")
			if fileExists(filepath.Join(destDir, binName)) {
				s.progressMap.Store(filename, 100)
				return nil
			}
			// 如果包存在但未解压完成，自动解压赋权
			_ = s.extractTarGz(destPath, destDir)
			_ = os.Chmod(filepath.Join(destDir, "zipalign"), 0755)
			_ = os.Chmod(filepath.Join(destDir, "zipalign_arm64"), 0755)
			s.progressMap.Store(filename, 100)
			return nil
		} else {
			jreDir := "jre-x64"
			if strings.Contains(filename, "arm64") {
				jreDir = "jre-arm64"
			}
			if fileExists(filepath.Join(s.toolsDir, jreDir, "bin", "java")) {
				s.progressMap.Store(filename, 100)
				return nil
			}
		}
	}

	// 针对国内环境，可以使用 ghproxy 等加速代理
	// 这里默认提供多源尝试，优先通过 ghproxy 代理加速下载
	mirrors := []string{
		"https://mirror.ghproxy.com/" + url,
		url,
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

	// 如果是 tar.gz 压缩包，立即触发后台解包释放 JRE 或原生工具链
	if strings.HasSuffix(filename, ".tar.gz") {
		if filename == "linux_tools.tar.gz" {
			s.progressMap.Store(filename, 95)
			destDir := filepath.Join(s.toolsDir, "linux_tools")
			err := s.extractTarGz(destPath, destDir)
			if err != nil {
				_ = os.Remove(destPath)
				return fmt.Errorf("解压 linux_tools 失败: %w", err)
			}
			// 给解压出来的可执行二进制赋权
			_ = os.Chmod(filepath.Join(destDir, "zipalign"), 0755)
			_ = os.Chmod(filepath.Join(destDir, "zipalign_arm64"), 0755)
		} else {
			jreDir := "jre-x64"
			if strings.Contains(filename, "arm64") {
				jreDir = "jre-arm64"
			}
			s.progressMap.Store(filename, 95) // 标记解压中
			err := s.extractTarGz(destPath, filepath.Join(s.toolsDir, jreDir))
			if err != nil {
				_ = os.Remove(destPath) // 损坏的包删除
				return fmt.Errorf("解压 JRE 失败: %w", err)
			}
			// 赋予 bin/java 可执行权限
			_ = os.Chmod(filepath.Join(s.toolsDir, jreDir, "bin", "java"), 0755)
		}
	}

	s.progressMap.Store(filename, 100)
	return nil
}

func (s *CustomService) extractTarGz(tarPath, destDir string) error {
	_ = os.RemoveAll(destDir)
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
		if strings.HasPrefix(cleaned, "..") || strings.HasPrefix(cleaned, "/") {
			continue
		}

		// 移除顶层公共目录以对齐 `--strip-components=1` 行为
		parts := strings.Split(cleaned, string(filepath.Separator))
		if len(parts) <= 1 {
			continue
		}
		targetRelative := filepath.Join(parts[1:]...)
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
	javaCmd := "java"

	arch := runtime.GOARCH
	jreDirName := "jre-x64"
	if arch == "arm64" {
		jreDirName = "jre-arm64"
	}
	localJava := filepath.Join(s.toolsDir, jreDirName, "bin", "java")

	if fileExists(localJava) {
		_ = os.Chmod(localJava, 0755)
		javaCmd = localJava
		s.appendLog("[INFO] 使用内置本地 JRE: %s", javaCmd)
	} else {
		s.appendLog("[WARN] 未找到本地 JRE，将自动降级尝试使用系统全局环境变量中的 'java' 命令")
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
		s.appendLog("[INFO] 已启用内嵌默认签名证书")
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

			// 3. 重新编译打包
			s.appendLog(">>> [4/6] 正在编译构建客户端应用...")
			rebuildArgs := []string{"-jar", apktoolJar, "b", "-c", "--use-aapt2", "-p", tempFrameDir, tempUnpackedDir, "-o", tempUnsignedApk}
			if err := ExecuteCommandWithLog(ctx, javaCmd, rebuildArgs, s.appendLog); err != nil {
				s.buildStatus = "failed"
				s.buildError = fmt.Sprintf("[%s] 编译客户端失败: %v", baseName, err)
				return
			}

			// 4. 对齐优化
			s.appendLog(">>> [5/6] 正在优化应用结构...")
			execCmd, flags := s.getZipalignCmd(javaCmd, s.appendLog)
			if execCmd != javaCmd {
				alignArgs := append(flags, tempUnsignedApk, tempAlignedApk)
				if err := ExecuteCommandWithLog(ctx, execCmd, alignArgs, s.appendLog); err != nil {
					s.buildStatus = "failed"
					s.buildError = fmt.Sprintf("[%s] 优化应用结构失败: %v", baseName, err)
					return
				}
			} else {
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
	if os.IsNotExist(err) {
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
	// 1. 检测本地 linux_tools/zipalign 可执行文件
	localBinName := "zipalign"
	if runtime.GOOS == "windows" {
		localBinName = "zipalign.exe"
	} else if runtime.GOARCH == "arm64" {
		if fileExists(filepath.Join(s.toolsDir, "linux_tools", "zipalign_arm64")) {
			localBinName = "zipalign_arm64"
		}
	}
	localBin := filepath.Join(s.toolsDir, "linux_tools", localBinName)
	if fileExists(localBin) {
		_ = os.Chmod(localBin, 0755)
		logFunc("[INFO] 优先使用原生二进制 zipalign: %s", localBin)
		return localBin, []string{"-p", "-f", "4"}
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
					binName := "zipalign"
					if runtime.GOOS == "windows" {
						binName = "zipalign.exe"
					}
					binPath := filepath.Join(sdkDir, entries[i].Name(), binName)
					if fileExists(binPath) {
						logFunc("[INFO] 检测到 Android SDK zipalign 工具: %s", binPath)
						return binPath, []string{"-p", "-f", "4"}
					}
				}
			}
		}
	}

	// 3. 检测系统环境变量 PATH 中的 zipalign
	binName := "zipalign"
	if runtime.GOOS == "windows" {
		binName = "zipalign.exe"
	}
	if path, err := exec.LookPath(binName); err == nil {
		logFunc("[INFO] 使用系统环境变量中的 zipalign: %s", path)
		return path, []string{"-p", "-f", "4"}
	}

	// 4. 退化
	return javaCmd, nil
}

// NativeZipAlign 纯 Go 语言实现的 Android 0xd990 规范 4 字节 Zip 边界对齐算法
type zipCountWriter struct {
	w     io.Writer
	count int64
}

func (cw *zipCountWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.count += int64(n)
	return n, err
}

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

	cw := &zipCountWriter{w: out}
	w := zip.NewWriter(cw)

	for _, f := range r.File {
		header := f.FileHeader
		cleanExtra := cleanAlignmentExtra(header.Extra)

		newHeader := &zip.FileHeader{
			Name:               header.Name,
			Comment:            header.Comment,
			NonUTF8:            header.NonUTF8,
			CreatorVersion:     header.CreatorVersion,
			ReaderVersion:      header.ReaderVersion,
			Flags:              header.Flags,
			Method:             header.Method,
			Modified:           header.Modified,
			CRC32:              header.CRC32,
			CompressedSize64:   header.CompressedSize64,
			UncompressedSize64: header.UncompressedSize64,
			Extra:              cleanExtra,
		}

		if header.Method == zip.Store {
			currentOffset := cw.count
			baseDataOffset := currentOffset + 30 + int64(len(newHeader.Name)) + int64(len(newHeader.Extra))
			padding := (align - (baseDataOffset % align)) % align

			if padding > 0 {
				if padding < 4 {
					padding += align
				}
				extraPadding := make([]byte, padding)
				binary.LittleEndian.PutUint16(extraPadding[0:2], 0xd990)
				binary.LittleEndian.PutUint16(extraPadding[2:4], uint16(padding-4))
				newHeader.Extra = append(newHeader.Extra, extraPadding...)
			}
		}

		targetWriter, err := w.CreateRaw(newHeader)
		if err != nil {
			return fmt.Errorf("创建 zip entry 失败 (%s): %w", header.Name, err)
		}

		rc, err := f.OpenRaw()
		if err != nil {
			return fmt.Errorf("读取源 zip entry 失败 (%s): %v", header.Name, err)
		}

		if _, err := io.Copy(targetWriter, rc); err != nil {
			return fmt.Errorf("写入 zip entry 失败 (%s): %v", header.Name, err)
		}
		if closer, ok := rc.(io.Closer); ok {
			_ = closer.Close()
		}
	}

	return w.Close()
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
