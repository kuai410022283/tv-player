package services

import (
	"fmt"
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

	resp, err := http.Get(source.URL)
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

	count, err := imp.importChannels(channels)
	if err == nil {
		// 更新特定源的同步时间
		_, _ = imp.channelSvc.db.Exec("UPDATE m3u_sources SET last_sync=? WHERE id=?", time.Now(), sourceID)
	}
	return count, err
}

// ImportFromString parses M3U content from a string
func (imp *M3UImporter) ImportFromString(content string) (int, error) {
	channels, err := ParseM3U(strings.NewReader(content))
	if err != nil {
		return 0, err
	}
	return imp.importChannels(channels)
}

func (imp *M3UImporter) importChannels(channels []map[string]string) (int, error) {
	groupCache := make(map[string]int64)
	imported := 0
	existingURLs := make(map[string]bool)

	// 使用数据库查询去重，而非加载全部频道到内存
	rows, err := imp.channelSvc.db.Query("SELECT stream_url FROM channels")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var url string
			if rows.Scan(&url) == nil {
				existingURLs[url] = true
			}
		}
	}

	// 开启事务进行批量插入，极大提升性能并防止锁死整个数据库
	tx, err := imp.channelSvc.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO channels (group_id, name, logo, stream_url, stream_type, epg_channel_id, status) VALUES (?, ?, ?, ?, ?, ?, 'unknown')`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	for _, ch := range channels {
		// 去重：跳过已存在的流地址
		if existingURLs[ch["url"]] {
			continue
		}

		groupName := ch["group-title"]
		if groupName == "" {
			groupName = "未分类"
		}

		groupID, ok := groupCache[groupName]
		if !ok {
			groups, _ := imp.channelSvc.ListGroups()
			found := false
			for _, g := range groups {
				if g.Name == groupName {
					groupID = g.ID
					found = true
					break
				}
			}
			if !found {
				newGroup := &models.ChannelGroup{Name: groupName, SortOrder: len(groups)}
				_ = imp.channelSvc.CreateGroup(newGroup)
				groupID = newGroup.ID
			}
			groupCache[groupName] = groupID
		}

		streamType := detectStreamType(ch["url"])
		_, err := stmt.Exec(groupID, ch["name"], ch["tvg-logo"], ch["url"], streamType, ch["tvg-id"])
		if err == nil {
			imported++
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
