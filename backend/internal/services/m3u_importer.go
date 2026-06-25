package services

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/models"
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
	// 1. 预先处理所有分组，按 "来源+分组名" 作为复合键隔离，不同来源的同名分组各自独立
	// 格式: "source|name" -> groupID
	groupCache := make(map[string]int64)
	existingGroups, _ := imp.channelSvc.ListGroups(0)
	for _, g := range existingGroups {
		cacheKey := g.Source + "|" + g.Name
		groupCache[cacheKey] = g.ID
	}

	for _, ch := range channels {
		groupName := ch["group-title"]
		if groupName == "" || groupName == "-" {
			if sourceName != "" {
				groupName = sourceName
			} else {
				groupName = "未分类"
			}
		}
		cacheKey := sourceName + "|" + groupName
		if _, ok := groupCache[cacheKey]; !ok {
			newGroup := &models.ChannelGroup{
				Name: groupName, 
				SortOrder: len(groupCache), 
				IsDirect: true,
				Source: sourceName,
				UserAgent: sourceUA,
				CustomHeaders: sourceHeaders,
			}
			if err := imp.channelSvc.CreateGroup(newGroup); err == nil {
				groupCache[cacheKey] = newGroup.ID
			}
		}
	}

	imported := 0

	// 2. 预处理 M3U 频道，按 "分组ID+频道名称" 合并流地址
	type mergedChannel struct {
		ch      map[string]string
		groupID int64
		urls    []string
	}
	mergedMap := make(map[string]*mergedChannel)
	// 用切片保持顺序，避免 map 遍历乱序
	var mergedKeys []string

	for _, ch := range channels {
		groupName := ch["group-title"]
		if groupName == "" || groupName == "-" {
			if sourceName != "" {
				groupName = sourceName
			} else {
				groupName = "未分类"
			}
		}
		cacheKey := sourceName + "|" + groupName
		groupID := groupCache[cacheKey]
		name := strings.TrimSpace(ch["name"])
		if name == "" {
			name = "未命名频道"
		}

		key := fmt.Sprintf("%d|%s", groupID, name)
		if existing, ok := mergedMap[key]; ok {
			// 避免重复追加相同 URL
			found := false
			for _, u := range existing.urls {
				if u == ch["url"] {
					found = true
					break
				}
			}
			if !found {
				existing.urls = append(existing.urls, ch["url"])
			}
		} else {
			mergedMap[key] = &mergedChannel{
				ch:      ch,
				groupID: groupID,
				urls:    []string{ch["url"]},
			}
			mergedKeys = append(mergedKeys, key)
		}
	}

	// 3. 读取数据库中该来源下已有的频道信息（用于匹配和镜像清理）
	existingDB := make(map[string]int64) // key -> channel_id
	keptIDs := make(map[int64]bool)
	rows, err := imp.channelSvc.db.Query("SELECT id, group_id, name FROM channels WHERE source = ?", sourceName)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, groupID int64
			var name string
			if rows.Scan(&id, &groupID, &name) == nil {
				key := fmt.Sprintf("%d|%s", groupID, name)
				existingDB[key] = id
			}
		}
	}

	// 4. 开启事务进行批量更新和插入
	tx, err := imp.channelSvc.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmtInsert, err := tx.Prepare(`INSERT INTO channels (group_id, name, logo, stream_url, stream_type, epg_channel_id, m3u_source_id, status, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days) VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmtInsert.Close()

	stmtUpdate, err := tx.Prepare(`UPDATE channels SET group_id = ?, name = ?, logo = ?, stream_url = ?, stream_type = ?, epg_channel_id = ?, m3u_source_id = ?, user_agent = ?, custom_headers = ?, support_catchup = ?, catchup_type = ?, catchup_source = ?, catchup_days = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmtUpdate.Close()

	for i, key := range mergedKeys {
		mc := mergedMap[key]
		ch := mc.ch
		groupID := mc.groupID
		mergedURLStr := strings.Join(mc.urls, "#")
		streamType := detectStreamType(mc.urls[0]) // 以第一条线路的类型为准

		// Build custom headers map
		headersMap := make(map[string]string)
		if sourceHeaders != "" {
			_ = json.Unmarshal([]byte(sourceHeaders), &headersMap)
		}
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
		if userAgent == "" {
			userAgent = sourceUA
		}
		supportCatchup := 0
		catchupType := ch["catchup"]
		if catchupType != "" {
			supportCatchup = 1
		}
		catchupSource := ch["catchup-source"]
		
		catchupDays := 0
		if days, err := strconv.Atoi(ch["catchup-days"]); err == nil && days > 0 {
			catchupDays = days
		} else if supportCatchup == 1 {
			catchupDays = 7
		}

		if channelID, exists := existingDB[key]; exists {
			// 更新已存在的频道
			_, _ = stmtUpdate.Exec(groupID, ch["name"], ch["tvg-logo"], mergedURLStr, streamType, ch["tvg-id"], sourceID, userAgent, customHeadersJSON, supportCatchup, catchupType, catchupSource, catchupDays, channelID)
			keptIDs[channelID] = true
		} else {
			// 插入新频道
			res, err := stmtInsert.Exec(groupID, ch["name"], ch["tvg-logo"], mergedURLStr, streamType, ch["tvg-id"], sourceID, sourceName, userAgent, customHeadersJSON, supportCatchup, catchupType, catchupSource, catchupDays)
			if err == nil {
				imported++
				if newID, err := res.LastInsertId(); err == nil {
					keptIDs[newID] = true
				}
			}
		}

		// 打印进度日志
		if (i+1)%100 == 0 || i == len(mergedKeys)-1 {
			slog.Info("M3U解析进度", "processed", i+1, "total", len(mergedKeys), "imported_new", imported)
		}
	}

	// 5. 镜像清理 (Purge) - 删除已在此次同步中消失的旧频道（仅限当前来源）
	for _, channelID := range existingDB {
		if !keptIDs[channelID] {
			_, _ = tx.Exec("DELETE FROM channels WHERE id = ?", channelID)
		}
	}

	// 6. 镜像清理 - 仅删除属于当前来源且名下没有频道的空分组 (保留 '未分类' 防呆，不误删其他来源的分组)
	_, _ = tx.Exec("DELETE FROM channel_groups WHERE name != '未分类' AND source = ? AND (SELECT COUNT(*) FROM channels WHERE group_id = channel_groups.id) = 0", sourceName)

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	return len(mergedKeys), nil
}

func detectStreamType(rawURL string) string {
	lowerURL := strings.ToLower(rawURL)

	// 1. 优先判断特殊协议
	if strings.HasPrefix(lowerURL, "rtmp://") {
		return "rtmp"
	}
	if strings.HasPrefix(lowerURL, "rtsp://") {
		return "rtsp"
	}

	// 2. 解析 URL
	u, err := url.Parse(rawURL)
	if err != nil {
		// 如果解析失败，采用最保守的降级匹配
		if strings.Contains(lowerURL, ".m3u8") {
			return "hls"
		}
		return "ts"
	}

	// 3. 从 Path 提取精确后缀
	ext := strings.ToLower(path.Ext(u.Path))
	switch ext {
	case ".m3u8":
		return "hls"
	case ".flv":
		return "flv"
	case ".mpd":
		return "dash"
	case ".mp4":
		return "mp4"
	case ".ts":
		return "ts"
	}

	// 4. 判断 udpxy 等组播特征路径
	lowerPath := strings.ToLower(u.Path)
	if strings.Contains(lowerPath, "/udp/") || strings.Contains(lowerPath, "/rtp/") {
		return "ts"
	}

	// 5. 降级：如果 Path 没有后缀，可能隐藏在 Query 参数中 (如 ?url=http://.../a.m3u8)
	lowerQuery := strings.ToLower(u.RawQuery)
	if strings.Contains(lowerQuery, ".m3u8") || strings.Contains(lowerQuery, "m3u8") {
		return "hls"
	}
	if strings.Contains(lowerQuery, ".flv") || strings.Contains(lowerQuery, "flv") {
		return "flv"
	}

	// 6. 无法确定，返回默认 ts
	return "ts"
}
