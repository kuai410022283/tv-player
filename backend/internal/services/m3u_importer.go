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
func (imp *M3UImporter) ImportFromURL(sourceID int64) (count int, err error) {
	_, _ = imp.channelSvc.db.Exec("UPDATE m3u_sources SET sync_status='syncing', sync_error='' WHERE id=?", sourceID)
	defer func() {
		if err != nil {
			_, _ = imp.channelSvc.db.Exec("UPDATE m3u_sources SET sync_status='error', sync_error=? WHERE id=?", err.Error(), sourceID)
		} else {
			_, _ = imp.channelSvc.db.Exec("UPDATE m3u_sources SET sync_status='idle', sync_error='', last_sync=? WHERE id=?", time.Now(), sourceID)
		}
	}()
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

	channels, epgURL, err := ParseM3U(resp.Body)
	if err != nil {
		return 0, err
	}

	if epgURL != "" {
		imp.appendEPGURL(epgURL)
	}

	count, err = imp.importChannels(channels, sourceID, source.Name, source.UserAgent, source.CustomHeaders)
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
	channels, epgURL, err := ParseM3U(strings.NewReader(content))
	if err != nil {
		return 0, err
	}
	if epgURL != "" {
		imp.appendEPGURL(epgURL)
	}
	return imp.importChannels(channels, 0, sourceName, "", "")
}

func (imp *M3UImporter) appendEPGURL(newURL string) {
	var current string
	err := imp.channelSvc.db.QueryRow("SELECT value FROM user_settings WHERE key='epg_source_url'").Scan(&current)
	if err != nil {
		// setting might not exist yet
		current = ""
	}

	// deduplicate
	urls := strings.Split(strings.ReplaceAll(current, "\r", "\n"), "\n")
	for _, u := range urls {
		if strings.TrimSpace(u) == newURL {
			return // already exists
		}
	}

	// Append
	if current != "" && !strings.HasSuffix(current, "\n") {
		current += "\n"
	}
	current += newURL

	_, _ = imp.channelSvc.db.Exec(`
		INSERT INTO user_settings (key, value) 
		VALUES ('epg_source_url', ?) 
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, current)
}

func (imp *M3UImporter) importChannels(channels []map[string]string, sourceID int64, sourceName string, sourceUA string, sourceHeaders string) (int, error) {
	// 1. 预先处理所有分组，按 "来源+分组名" 作为复合键隔离，不同来源的同名分组各自独立
	// 格式: "source|name" -> groupID
	groupCache := make(map[string]int64)
	existingGroups, _ := imp.channelSvc.ListGroups(0, true)
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
				Name:          groupName,
				SortOrder:     len(groupCache),
				IsDirect:      true,
				Source:        sourceName,
				UserAgent:     sourceUA,
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

	// 3.5 获取当前数据库中各分组的最大 sort_order，以便新频道能在分组末尾追加
	maxOrders := make(map[int64]int)
	rowsMax, err := imp.channelSvc.db.Query("SELECT group_id, COALESCE(MAX(sort_order), -1) FROM channels WHERE source = ? GROUP BY group_id", sourceName)
	if err == nil {
		defer rowsMax.Close()
		for rowsMax.Next() {
			var gID int64
			var m int
			if rowsMax.Scan(&gID, &m) == nil {
				maxOrders[gID] = m
			}
		}
	}

	// 4. 开启事务进行批量更新和插入
	tx, err := imp.channelSvc.db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	stmtInsert, err := tx.Prepare(`INSERT INTO channels (group_id, name, logo, stream_url, stream_type, epg_channel_id, m3u_source_id, status, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days, content_type, fcc, fcc_type, sort_order) VALUES (?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmtInsert.Close()

	stmtUpdate, err := tx.Prepare(`UPDATE channels SET group_id = ?, name = ?, logo = ?, stream_url = ?, stream_type = ?, epg_channel_id = ?, m3u_source_id = ?, user_agent = ?, custom_headers = ?, support_catchup = ?, catchup_type = ?, catchup_source = ?, catchup_days = ?, fcc = ?, fcc_type = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`)
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

		fcc := ch["fcc"]
		fccType := ch["fcc-type"]

		if channelID, exists := existingDB[key]; exists {
			// 更新已存在的频道
			_, _ = stmtUpdate.Exec(groupID, ch["name"], ch["tvg-logo"], mergedURLStr, streamType, ch["tvg-id"], sourceID, userAgent, customHeadersJSON, supportCatchup, catchupType, catchupSource, catchupDays, fcc, fccType, channelID)
			keptIDs[channelID] = true
		} else {
			// 插入新频道，使用分组独立的计数器
			currentMax, ok := maxOrders[groupID]
			if !ok {
				currentMax = -1
			}
			sortOrder := currentMax + 1
			maxOrders[groupID] = sortOrder

			res, err := stmtInsert.Exec(groupID, ch["name"], ch["tvg-logo"], mergedURLStr, streamType, ch["tvg-id"], sourceID, sourceName, userAgent, customHeadersJSON, supportCatchup, catchupType, catchupSource, catchupDays, fcc, fccType, sortOrder)
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

	// 7. 消除所有因旧频道删除而产生的排序数字空洞，同时整理新导入频道的最终分组内顺序
	_ = imp.channelSvc.ReorderChannels(0, sourceName)

	return imported, nil
}

func detectStreamType(rawURL string) string {
	lowerURL := strings.ToLower(rawURL)

	// 1. 优先判断特殊非 HTTP 协议（精确前缀匹配，无误判风险）
	if strings.HasPrefix(lowerURL, "rtmp://") {
		return "rtmp"
	}
	if strings.HasPrefix(lowerURL, "rtsp://") {
		return "rtsp"
	}
	// 明确识别 UDP/RTP 组播协议（原先隐式兜底为 ts，现在显式声明）
	if strings.HasPrefix(lowerURL, "udp://") || strings.HasPrefix(lowerURL, "rtp://") {
		return "ts"
	}

	// 2. 解析 URL 结构，提取路径和参数
	u, err := url.Parse(rawURL)
	if err != nil {
		// URL 格式非法，保守降级：只认 .m3u8 后缀，其余返回空（未知）
		if strings.Contains(lowerURL, ".m3u8") {
			return "hls"
		}
		return ""
	}

	// 3. 从 Path 提取精确文件后缀（最可靠的判断手段）
	uPath := u.Path
	// 本地裸路径（如 /vol1/media/video.mp4）在某些边缘场景下 url.Parse 可能不填充 Path，
	// 若检测到是本地路径且 Path 为空，回退使用原始输入提取后缀
	if uPath == "" && isLocalPath(rawURL) {
		uPath = rawURL
	}
	ext := strings.ToLower(path.Ext(uPath))
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
	case ".mkv":
		return "mkv"
	case ".avi":
		return "avi"
	}

	// 4. 判断 udpxy 等组播代理网关的特征路径（如 /udp/232.x.x.x:1234）
	lowerPath := strings.ToLower(u.Path)
	if strings.Contains(lowerPath, "/udp/") || strings.Contains(lowerPath, "/rtp/") {
		return "ts"
	}

	// 5. 结构化解析 Query 参数值，避免原始字符串匹配的误判
	//    例如：?noflv=true 原先会误识别为 flv，现在只匹配参数值中的文件后缀
	if qParams, qErr := url.ParseQuery(u.RawQuery); qErr == nil {
		for _, vals := range qParams {
			for _, v := range vals {
				lv := strings.ToLower(v)
				// 参数值本身是一个指向 m3u8 或 flv 的 URL/路径
				if strings.HasSuffix(lv, ".m3u8") || strings.Contains(lv, "m3u8") {
					return "hls"
				}
				if strings.HasSuffix(lv, ".flv") {
					return "flv"
				}
			}
		}
	}

	// 6. 无法从 URL 静态推断类型，返回空字符串
	//    调用方应通过 Content-Type 运行时检测（Layer 2）来补全类型
	return ""
}
