package services

import (
	"compress/gzip"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

type EPGService struct {
	db *sql.DB
}

func NewEPGService(db *sql.DB) *EPGService {
	return &EPGService{db: db}
}

// ── XMLTV 解析结构体 ──

type xmltvTV struct {
	XMLName    xml.Name         `xml:"tv"`
	Channels   []xmltvChannel   `xml:"channel"`
	Programmes []xmltvProgramme `xml:"programme"`
}

type xmltvChannel struct {
	ID          string   `xml:"id,attr"`
	DisplayName []string `xml:"display-name"`
}

type xmltvProgramme struct {
	Start   string `xml:"start,attr"`
	Stop    string `xml:"stop,attr"`
	Channel string `xml:"channel,attr"`
	Title   struct {
		Value string `xml:",chardata"`
	} `xml:"title"`
	Desc struct {
		Value string `xml:",chardata"`
	} `xml:"desc"`
}

// ── 内存缓存结构 ──

type epgIndex struct {
	mu       sync.RWMutex
	programs map[string]map[string][]models.EPGProgram // channelIDLower -> date("2006-01-02") -> programs
}

var globalEPGIndex = &epgIndex{
	programs: make(map[string]map[string][]models.EPGProgram),
}

// xmltv 时间格式 "20060102150405 -0700"
func parseXmltvTime(s string) (time.Time, error) {
	if len(s) >= 14 {
		// 尝试带时区
		t, err := time.Parse("20060102150405 -0700", s)
		if err == nil {
			return t, nil
		}
		// 尝试无时区
		t, err = time.Parse("20060102150405", s[:14])
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time format: %s", s)
}

// ── 后台任务与解析逻辑 ──

func (s *EPGService) StartEPGScheduler() {
	go func() {
		// 启动时延迟 3 秒拉取，避免阻塞主程序
		time.Sleep(3 * time.Second)
		s.FetchAndBuildIndex()

		ticker := time.NewTicker(time.Hour)
		for range ticker.C {
			// 定时检查是否需要刷新
			s.FetchAndBuildIndex()
		}
	}()
}

var lastFetchTime time.Time

func (s *EPGService) FetchAndBuildIndex() {
	var sourceURL string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_source_url'`).Scan(&sourceURL)
	if err != nil || sourceURL == "" {
		return // 未配置 EPG
	}

	refreshHours := 12
	var refreshHoursStr string
	if err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_refresh_hours'`).Scan(&refreshHoursStr); err == nil && refreshHoursStr != "" {
		fmt.Sscanf(refreshHoursStr, "%d", &refreshHours)
	}
	if refreshHours <= 0 {
		refreshHours = 12
	}

	// 检查是否需要刷新
	if time.Since(lastFetchTime) < time.Duration(refreshHours)*time.Hour {
		return
	}

	slog.Info("开始拉取 EPG 数据", "url", sourceURL)
	
	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Get(sourceURL)
	if err != nil {
		slog.Error("EPG 拉取失败", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("EPG 拉取失败", "status", resp.StatusCode)
		return
	}

	var reader io.Reader = resp.Body
	isGz := strings.HasSuffix(strings.ToLower(sourceURL), ".gz") || strings.Contains(resp.Header.Get("Content-Encoding"), "gzip")
	if isGz {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			slog.Error("EPG Gzip 解压失败", "error", err)
			return
		}
		defer gr.Close()
		reader = gr
	}

	decoder := xml.NewDecoder(reader)
	var tv xmltvTV
	if err := decoder.Decode(&tv); err != nil {
		slog.Error("EPG XML 解析失败", "error", err)
		return
	}

	newIndex := make(map[string]map[string][]models.EPGProgram)

	for _, prog := range tv.Programmes {
		start, err1 := parseXmltvTime(prog.Start)
		stop, err2 := parseXmltvTime(prog.Stop)
		if err1 != nil || err2 != nil {
			continue
		}

		chID := strings.ToLower(prog.Channel)
		if _, ok := newIndex[chID]; !ok {
			newIndex[chID] = make(map[string][]models.EPGProgram)
		}

		dateKey := start.In(time.Local).Format("2006-01-02")
		p := models.EPGProgram{
			ChannelID: chID,
			Title:     prog.Title.Value,
			StartTime: start,
			EndTime:   stop,
			Desc:      prog.Desc.Value,
		}
		newIndex[chID][dateKey] = append(newIndex[chID][dateKey], p)
	}

	// 排序
	for ch, dateMap := range newIndex {
		for date, progs := range dateMap {
			sort.Slice(progs, func(i, j int) bool {
				return progs[i].StartTime.Before(progs[j].StartTime)
			})
			newIndex[ch][date] = progs
		}
	}

	// 建立 channel display-name 到 id 的额外映射，提升模糊匹配成功率
	for _, ch := range tv.Channels {
		for _, dn := range ch.DisplayName {
			dnLower := strings.ToLower(dn)
			if _, ok := newIndex[dnLower]; !ok {
				if progs, exists := newIndex[strings.ToLower(ch.ID)]; exists {
					newIndex[dnLower] = progs
				}
			}
		}
	}

	globalEPGIndex.mu.Lock()
	globalEPGIndex.programs = newIndex
	globalEPGIndex.mu.Unlock()

	lastFetchTime = time.Now()
	slog.Info("EPG 数据更新完成", "channels", len(newIndex))
}

// GetEPG 从内存索引中获取当天的 EPG
func (s *EPGService) GetEPG(channelID string, date string) []models.EPGProgram {
	globalEPGIndex.mu.RLock()
	defer globalEPGIndex.mu.RUnlock()

	chID := strings.ToLower(channelID)
	
	// 尝试精确匹配
	if dateMap, ok := globalEPGIndex.programs[chID]; ok {
		if progs, ok2 := dateMap[date]; ok2 {
			return progs
		}
	}
	
	// 尝试模糊匹配（剔除横杠和空格，比如 CCTV-1 等于 cctv1）
	chIDClean := strings.ReplaceAll(chID, "-", "")
	chIDClean = strings.ReplaceAll(chIDClean, " ", "")
	
	for key, dateMap := range globalEPGIndex.programs {
		keyClean := strings.ReplaceAll(key, "-", "")
		keyClean = strings.ReplaceAll(keyClean, " ", "")
		if keyClean == chIDClean {
			if progs, ok2 := dateMap[date]; ok2 {
				return progs
			}
		}
	}

	return []models.EPGProgram{}
}

// ForceRefresh 强制刷新
func (s *EPGService) ForceRefresh() error {
	lastFetchTime = time.Time{} // 重置时间
	s.FetchAndBuildIndex()
	return nil
}
