package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

type M3UImporter struct {
	channelSvc *ChannelService
}

func NewM3UImporter(channelSvc *ChannelService) *M3UImporter {
	return &M3UImporter{channelSvc: channelSvc}
}

// ImportFromURL fetches and imports an M3U source
func (imp *M3UImporter) ImportFromURL(sourceID int64) (int, error) {
	sources, err := imp.channelSvc.ListM3USources()
	if err != nil {
		return 0, err
	}

	var source *models.M3USource
	for _, s := range sources {
		if s.ID == sourceID {
			source = &s
			break
		}
	}
	if source == nil {
		return 0, fmt.Errorf("source not found: %d", sourceID)
	}

	// 校验 M3U 源 URL
	if err := ValidateStreamURL(source.URL); err != nil {
		return 0, fmt.Errorf("M3U 源地址不安全: %w", err)
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequest("GET", source.URL, nil)
	if err != nil {
		return 0, err
	}
	if source.UserAgent != "" {
		req.Header.Set("User-Agent", source.UserAgent)
	} else {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player")
	}
	if source.CustomHeaders != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(source.CustomHeaders), &headers); err == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}

	// 检查 HTTP 响应状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		resp.Body.Close()
		return 0, fmt.Errorf("M3U 源返回错误状态码: %d", resp.StatusCode)
	}

	defer resp.Body.Close()

	channels, err := ParseM3U(resp.Body)
	if err != nil {
		return 0, err
	}

	count, err := imp.importChannels(channels, sourceID, source.Name, source.UserAgent, source.CustomHeaders)
	if err == nil {
		// 更新特定源的同步时间
		_, _ = imp.channelSvc.db.Exec("UPDATE m3u_sources SET last_sync=? WHERE id=?", time.Now(), sourceID)
	}
	return count, err
}

// StartAutoSync starts a background goroutine to automatically sync M3U sources
func (imp *M3UImporter) StartAutoSync() {
	go func() {
		// Run every hour to check which sources need syncing
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			sources, err := imp.channelSvc.ListM3USources()
			if err != nil {
				continue
			}
			for _, s := range sources {
				if s.AutoSync {
					interval := s.SyncInterval
					if interval <= 0 {
						interval = 12
					}
					// Only sync if enough time has passed since last sync
					if time.Since(s.LastSync).Hours() >= float64(interval) {
						_, _ = imp.ImportFromURL(s.ID)
					}
				}
			}
		}
	}()
}

// ImportFromString parses M3U content from a string
func (imp *M3UImporter) ImportFromString(content string, sourceName string) (int, error) {
	channels, err := ParseM3U(strings.NewReader(content))
	if err != nil {
		return 0, err
	}
	return imp.importChannels(channels, 0, sourceName, "", "")
}

func (imp *M3UImporter) importChannels(channels []map[string]string, sourceID int64, sourceName string, sourceUA string, sourceHeaders string) (int, error) {
	// 1. 预先处理所有分组，避免在开启频道的写入事务后再执行其它表写入，导致 SQLite 锁表(Deadlock)
	groupCache := make(map[string]int64)
	existingGroups, _ := imp.channelSvc.ListGroups(0)
	for _, g := range existingGroups {
		if g.Source == sourceName {
			groupCache[g.Name] = g.ID
		}
	}

	for _, ch := range channels {
		groupName := ch["group-title"]
		if groupName == "" {
			groupName = "未分类"
		}
		if _, ok := groupCache[groupName]; !ok {
			newGroup := &models.ChannelGroup{
				Name: groupName, 
				SortOrder: len(groupCache), 
				Source: sourceName,
				UserAgent: sourceUA,
				CustomHeaders: sourceHeaders,
			}
			if err := imp.channelSvc.CreateGroup(newGroup); err == nil {
				groupCache[groupName] = newGroup.ID
			}
		}
	}

	imported := 0
	existingURLs := make(map[string]bool)

	// 2. 使用数据库查询去重，仅检查当前来源下的频道
	rows, err := imp.channelSvc.db.Query("SELECT stream_url FROM channels WHERE source = ?", sourceName)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var url string
			if rows.Scan(&url) == nil {
				existingURLs[url] = true
			}
		}
	}

	// 3. 开启事务进行批量更新和插入
	tx, err := imp.channelSvc.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmtInsert, err := tx.Prepare(`INSERT INTO channels (group_id, name, logo, stream_url, stream_type, epg_channel_id, m3u_source_id, status, source, user_agent, custom_headers) VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmtInsert.Close()

	stmtUpdate, err := tx.Prepare(`UPDATE channels SET group_id = ?, name = ?, logo = ?, stream_type = ?, epg_channel_id = ?, m3u_source_id = ?, user_agent = ?, custom_headers = ?, updated_at = CURRENT_TIMESTAMP WHERE stream_url = ? AND source = ?`)
	if err != nil {
		return 0, err
	}
	defer stmtUpdate.Close()

	for i, ch := range channels {
		groupName := ch["group-title"]
		if groupName == "" {
			groupName = "未分类"
		}
		groupID := groupCache[groupName]

		streamType := detectStreamType(ch["url"])

		// Build custom headers map
		headersMap := make(map[string]string)
		if ref, ok := ch["http-referrer"]; ok && ref != "" {
			headersMap["Referer"] = ref
		}
		if orig, ok := ch["http-origin"]; ok && orig != "" {
			headersMap["Origin"] = orig
		}
		
		customHeadersJSON := ""
		if len(headersMap) > 0 {
			if b, err := json.Marshal(headersMap); err == nil {
				customHeadersJSON = string(b)
			}
		}
		
		userAgent := ch["user_agent"]

		if existingURLs[ch["url"]] {
			// 更新已存在的频道
			_, _ = stmtUpdate.Exec(groupID, ch["name"], ch["tvg-logo"], streamType, ch["tvg-id"], sourceID, userAgent, customHeadersJSON, ch["url"], sourceName)
		} else {
			// 插入新频道
			_, err := stmtInsert.Exec(groupID, ch["name"], ch["tvg-logo"], ch["url"], streamType, ch["tvg-id"], sourceID, sourceName, userAgent, customHeadersJSON)
			if err == nil {
				imported++
				existingURLs[ch["url"]] = true // 防止同一个 M3U 里有重复 URL 导致后续插入失败
			}
		}
		
		// 打印进度日志 (每 100 个打印一次，或最后一个打印一次)
		if (i+1)%100 == 0 || i == len(channels)-1 {
			slog.Info("M3U解析进度", "parsed", i+1, "total", len(channels), "imported", imported)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return imported, nil
}

func detectStreamType(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, ".m3u8") || strings.Contains(lower, "m3u8"):
		return "hls"
	case strings.Contains(lower, ".flv"):
		return "flv"
	case strings.HasPrefix(lower, "rtmp://"):
		return "rtmp"
	case strings.HasPrefix(lower, "rtsp://"):
		return "rtsp"
	case strings.Contains(lower, ".mpd"):
		return "dash"
	case strings.Contains(lower, ".mp4"):
		return "mp4"
	case strings.Contains(lower, "/udp/") || strings.Contains(lower, "/rtp/") || strings.HasSuffix(lower, ".ts"):
		return "ts"
	default:
		return "hls"
	}
}
