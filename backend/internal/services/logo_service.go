package services

import (
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type LogoService struct {
	db      *sql.DB
	logoDir string

	mu       sync.RWMutex
	fileMap  map[string]string // cleanName -> actual filename
	lastScan time.Time
}

func NewLogoService(db *sql.DB) *LogoService {
	dir := "./library/channel_logo"
	if err := os.MkdirAll(dir, 0755); err != nil {
		slog.Error("Failed to create logo directory", "error", err)
	}
	s := &LogoService{
		db:      db,
		logoDir: dir,
		fileMap: make(map[string]string),
	}
	s.scanDirectory()
	return s
}

func (s *LogoService) scanDirectory() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 限制扫描频率，避免高并发频繁扫描硬盘
	if time.Since(s.lastScan) < 5*time.Second {
		return
	}
	s.lastScan = time.Now()

	entries, err := os.ReadDir(s.logoDir)
	if err != nil {
		return
	}

	newMap := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		clean := s.CleanName(base)
		// 优先记录，如果是覆盖，后面出现的同cleanName会覆盖前面的
		newMap[clean] = name
	}
	s.fileMap = newMap
}

// CleanName 清洗频道名称，用于本地存储文件名匹配
func (s *LogoService) CleanName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	
	// 移除常见后缀
	suffixes := []string{"hd", "4k", "fhd", "超清", "高清", "频道", "测试"}
	for _, suffix := range suffixes {
		name = strings.ReplaceAll(name, suffix, "")
	}

	// 移除标点和空格
	re := regexp.MustCompile(`[\s+\-\.,_\[\]\(\)\*★\+]`)
	name = re.ReplaceAllString(name, "")

	return name
}

func (s *LogoService) GetLogoPath(cleanName string) string {
	s.mu.RLock()
	actualName, exists := s.fileMap[cleanName]
	s.mu.RUnlock()

	if !exists {
		// 没找到尝试扫描一次最新目录
		s.scanDirectory()
		s.mu.RLock()
		actualName, exists = s.fileMap[cleanName]
		s.mu.RUnlock()
	}

	if exists {
		return filepath.Join(s.logoDir, actualName)
	}
	// fallback
	return filepath.Join(s.logoDir, cleanName+".png")
}

func (s *LogoService) HasLocalLogo(cleanName string) bool {
	s.mu.RLock()
	_, exists := s.fileMap[cleanName]
	s.mu.RUnlock()

	if !exists {
		s.scanDirectory()
		s.mu.RLock()
		_, exists = s.fileMap[cleanName]
		s.mu.RUnlock()
	}

	if exists {
		return true
	}

	path := filepath.Join(s.logoDir, cleanName+".png")
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// downloadImage 下载并落地图片
func (s *LogoService) downloadImage(url, cleanName string, overwrite bool) bool {
	path := s.GetLogoPath(cleanName)
	if !overwrite && s.HasLocalLogo(cleanName) {
		return false
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Ensure Content-Type is an image
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "image/") {
		return false
	}

	out, err := os.Create(path)
	if err != nil {
		slog.Error("Failed to create logo file", "path", path, "error", err)
		return false
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		slog.Error("Failed to write logo file", "path", path, "error", err)
		return false
	}

	// 记录到内存映射中，避免等待下次扫描
	s.mu.Lock()
	s.fileMap[cleanName] = cleanName + ".png"
	s.mu.Unlock()

	return true
}

// CacheExistingLogos 后台任务：将频道表中现有的外部 logo 链接缓存到本地
func (s *LogoService) CacheExistingLogos() {
	slog.Info("开始后台缓存现有台标...")
	rows, err := s.db.Query("SELECT name, logo, COALESCE(epg_channel_id, '') FROM channels WHERE logo != '' AND logo LIKE 'http%'")
	if err != nil {
		slog.Error("CacheExistingLogos db query failed", "error", err)
		return
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var name, logoURL, epgID string
		if err := rows.Scan(&name, &logoURL, &epgID); err != nil {
			continue
		}
		
		// 按照需求：先拿到 epg_channel_id，如果为空则跳过
		if epgID == "" {
			continue
		}

		cleanName := s.CleanName(epgID)
		if s.downloadImage(logoURL, cleanName, false) {
			count++
		}
	}
	slog.Info("缓存现有台标完成", "downloaded", count)
}

// FetchLogosFromSources 后台任务：从全局设置的源库中拉取缺失台标
func (s *LogoService) FetchLogosFromSources(overwrite bool) {
	slog.Info("开始从源库批量拉取台标...")
	
	var urlsStr string
	err := s.db.QueryRow("SELECT value FROM user_settings WHERE key = 'local_logo_urls'").Scan(&urlsStr)
	if err != nil || strings.TrimSpace(urlsStr) == "" {
		slog.Warn("未配置台标源库地址，取消拉取")
		return
	}

	sources := strings.Split(urlsStr, "\n")
	var validSources []string
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source != "" {
			if !strings.HasSuffix(source, "/") {
				source += "/"
			}
			validSources = append(validSources, source)
		}
	}

	if len(validSources) == 0 {
		return
	}

	rows, err := s.db.Query("SELECT name, COALESCE(epg_channel_id, '') FROM channels")
	if err != nil {
		slog.Error("FetchLogosFromSources db query failed", "error", err)
		return
	}
	defer rows.Close()

	type chanInfo struct {
		Name  string
		EpgID string
	}
	var channels []chanInfo
	for rows.Next() {
		var name, epg string
		if err := rows.Scan(&name, &epg); err != nil {
			continue
		}
		channels = append(channels, chanInfo{Name: name, EpgID: epg})
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制10个并发

	successCount := 0
	var mu sync.Mutex

	for _, ch := range channels {
		wg.Add(1)
		go func(c chanInfo) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			if c.EpgID == "" {
				return
			}

			cleanName := s.CleanName(c.EpgID)
			if !overwrite && s.HasLocalLogo(cleanName) {
				return
			}

			// 尝试所有源
			for _, source := range validSources {
				// 源格式假设为：http://example.com/logo/cctv1.png
				// 如果源地址是其他格式（如 .webp），也可以按需补充。本处先默认尝试 .png。
				testURL := source + cleanName + ".png"
				if s.downloadImage(testURL, cleanName, overwrite) {
					mu.Lock()
					successCount++
					mu.Unlock()
					break // 成功获取，跳出源尝试
				}
			}
		}(ch)
	}

	wg.Wait()
	slog.Info("从源库拉取台标完成", "downloaded", successCount)
}

// GetLogoStrategy 获取当前的台标策略
func (s *LogoService) GetLogoStrategy() string {
	var strategy string
	err := s.db.QueryRow("SELECT value FROM user_settings WHERE key = 'logo_strategy'").Scan(&strategy)
	if err == nil && strategy != "" {
		return strategy
	}

	var enableLocal string
	err = s.db.QueryRow("SELECT value FROM user_settings WHERE key = 'enable_local_logo'").Scan(&enableLocal)
	if err == nil && enableLocal == "true" {
		return "local"
	}
	return "source" // 默认策略
}

// ResolveLogo 解析并返回最符合策略的台标 URL
func (s *LogoService) ResolveLogo(name, epg, dbLogo string, id int64, strategy string, baseURL string) string {
	cleanName := s.CleanName(name)
	var cleanEPG string
	if epg != "" {
		cleanEPG = s.CleanName(epg)
	}

	// 检查本地台标是否存在
	hasLocal := false
	localClean := cleanName
	if cleanEPG != "" && s.HasLocalLogo(cleanEPG) {
		hasLocal = true
		localClean = cleanEPG
	} else if s.HasLocalLogo(cleanName) {
		hasLocal = true
	}

	// 格式化本地台标 URL
	formatLocalURL := func() string {
		escaped := url.QueryEscape(localClean)
		path := fmt.Sprintf("/api/v1/logo?name=%s&id=%d", escaped, id)
		if baseURL != "" {
			return baseURL + path
		}
		return path
	}

	// 格式化数据库台标 URL
	formatDBURL := func() string {
		if strings.HasPrefix(dbLogo, "http://") || strings.HasPrefix(dbLogo, "https://") {
			return dbLogo
		}
		if baseURL != "" {
			return baseURL + dbLogo
		}
		return dbLogo
	}

	// 格式化固定兜底台标 URL
	formatDefaultURL := func() string {
		path := "/library/channel_logo/default.png"
		if baseURL != "" {
			return baseURL + path
		}
		return path
	}

	switch strategy {
	case "local":
		// 本地优先：本地 》 数据库 》 接口
		if hasLocal {
			return formatLocalURL()
		}
		if dbLogo != "" {
			return formatDBURL()
		}
		// 接口
		return formatLocalURL()

	case "source":
		// 源优先：数据库 》 本地 》 接口
		if dbLogo != "" {
			return formatDBURL()
		}
		if hasLocal {
			return formatLocalURL()
		}
		// 接口
		return formatLocalURL()

	case "interface":
		// 接口优先：接口 》 数据库 》 本地
		return formatLocalURL()

	default:
		return formatDefaultURL()
	}
}

