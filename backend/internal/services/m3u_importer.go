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

	resp, err := http.Get(source.URL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	channels, err := ParseM3U(resp.Body)
	if err != nil {
		return 0, err
	}

	count, err := imp.importChannels(channels)
	if err == nil {
		// 更新特定源的同步时间
		imp.channelSvc.db.Exec("UPDATE m3u_sources SET last_sync=? WHERE id=?", time.Now(), sourceID)
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

	// 获取已有频道的 URL 列表用于去重
	if existing, err := imp.channelSvc.ListChannels(0, false, "", &models.PageRequest{Page: 1, PageSize: 10000}); err == nil {
		if items, ok := existing.Items.([]models.Channel); ok {
			for _, ch := range items {
				existingURLs[ch.StreamURL] = true
			}
		}
	}

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
				imp.channelSvc.CreateGroup(newGroup)
				groupID = newGroup.ID
			}
			groupCache[groupName] = groupID
		}

		streamType := detectStreamType(ch["url"])
		channel := &models.Channel{
			GroupID:      groupID,
			Name:         ch["name"],
			Logo:         ch["tvg-logo"],
			StreamURL:    ch["url"],
			StreamType:   streamType,
			EPGChannelID: ch["tvg-id"],
		}
		if err := imp.channelSvc.CreateChannel(channel); err == nil {
			imported++
		}
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
	default:
		return "hls"
	}
}
