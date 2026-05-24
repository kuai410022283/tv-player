package services

import (
	"compress/gzip"
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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
		// 注意：Go 的时间解析模板必须使用 "-0700" 来代表时区，不能写 "+0800"
		t, err := time.Parse("20060102150405 -0700", s)
		if err == nil {
			return t, nil
		}
		// 尝试无时区，默认按照东八区解析
		loc := time.FixedZone("CST", 8*3600)
		t, err = time.ParseInLocation("20060102150405", s[:14], loc)
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

// normalizeChannelName 标准化频道名称，专门处理 CCTV 频道的各种变体
func normalizeChannelName(s string) string {
	// 1. URL 解码 (处理类似 CCTV-10%20%E7%A7%91%E6%95%99 的情况)
	if unescaped, err := url.QueryUnescape(s); err == nil {
		s = unescaped
	}

	// 2. 转小写并去除常见的分隔符和空格
	s = strings.ToLower(s)
	// 将全角加号替换为半角加号
	s = strings.ReplaceAll(s, "＋", "+")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")

	// 3. 针对 cctv 频道的特殊提取逻辑
	if strings.HasPrefix(s, "cctv") {
		// 提取 cctv 后面的数字，以及可能带有的 '+' 或 'k'（比如 cctv5+, cctv4k）
		prefix := "cctv"
		idx := 4
		hasNumber := false
		for idx < len(s) && s[idx] >= '0' && s[idx] <= '9' {
			prefix += string(s[idx])
			idx++
			hasNumber = true
		}

		if hasNumber {
			// 提取后缀
			if idx < len(s) && s[idx] == '+' {
				prefix += "+"
			} else if idx < len(s) && s[idx] == 'k' {
				prefix += "k"
			}

			// 针对 CCTV5+ 、CCTV5Plus 等别名处理
			if prefix == "cctv5" && (strings.Contains(s, "plus") || strings.Contains(s, "赛事")) {
				prefix = "cctv5+"
			}

			// 针对 CCTV-4，需要保留欧洲、美洲等地域区分
			if prefix == "cctv4" {
				if strings.Contains(s, "欧") || strings.Contains(s, "europe") || strings.Contains(s, "euo") || strings.Contains(s, "euro") {
					prefix += "欧洲"
				} else if strings.Contains(s, "美") || strings.Contains(s, "america") || strings.Contains(s, "ame") {
					prefix += "美洲"
				}
				// 亚洲/中文国际版通常作为默认版，不再加后缀
			}

			// 对于 CCTV4K，保留测试频道的差异
			if prefix == "cctv4k" && strings.Contains(s, "测试") {
				prefix += "测试"
			}

			return prefix // 统一返回标准格式
		} else {
			// 处理类似 "cctv新闻" 没有数字的纯文字后缀情况
			if strings.Contains(s, "新闻") {
				return "cctv13"
			}
			if strings.Contains(s, "少儿") {
				return "cctv14"
			}
			if strings.Contains(s, "音乐") {
				return "cctv15"
			}
		}
	}

	// 4. 针对 CGTN 频道的特殊提取逻辑
	if strings.HasPrefix(s, "cgtn") {
		prefix := "cgtn"
		if strings.Contains(s, "俄") || strings.Contains(s, "russian") {
			prefix += "俄语"
		} else if strings.Contains(s, "法") || strings.Contains(s, "french") {
			prefix += "法语"
		} else if strings.Contains(s, "西") || strings.Contains(s, "spanish") {
			prefix += "西班牙语"
		} else if strings.Contains(s, "阿") || strings.Contains(s, "arabic") {
			prefix += "阿拉伯语"
		} else if strings.Contains(s, "纪录") || strings.Contains(s, "doc") {
			prefix += "纪录"
		} else {
			// 如果没有上述后缀，通常就是默认的英文新闻频道
			prefix += "新闻"
		}
		return prefix
	}

	return s
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

	// 尝试基于统一规则的模糊匹配
	chIDClean := normalizeChannelName(channelID)

	for key, dateMap := range globalEPGIndex.programs {
		keyClean := normalizeChannelName(key)
		if keyClean == chIDClean {
			if progs, ok2 := dateMap[date]; ok2 {
				return progs
			}
		}
	}

	return []models.EPGProgram{}
}

// GetCurrentEPGWithProgress 从内存索引中获取当前正在播放的节目名称和进度百分比
func (s *EPGService) GetCurrentEPGWithProgress(channelID string) (string, int) {
	globalEPGIndex.mu.RLock()
	defer globalEPGIndex.mu.RUnlock()

	chIDClean := normalizeChannelName(channelID)
	now := time.Now()

	// 找到对应的频道的所有日期 EPG
	var targetDateMap map[string][]models.EPGProgram
	chID := strings.ToLower(channelID)
	if dateMap, ok := globalEPGIndex.programs[chID]; ok {
		targetDateMap = dateMap
	} else {
		for key, dateMap := range globalEPGIndex.programs {
			if normalizeChannelName(key) == chIDClean {
				targetDateMap = dateMap
				break
			}
		}
	}

	if targetDateMap == nil {
		return "", 0
	}

	// 不再依赖 time.Now() 格式化后的 dateStr，因为如果服务器是 UTC 而 EPG 是 +0800 会导致跨天时取不到数据
	// 直接遍历该频道的全部日期（通常只有 3-7 天），使用 UTC 绝对时间匹配
	for _, progs := range targetDateMap {
		for _, p := range progs {
			if now.After(p.StartTime) && now.Before(p.EndTime) {
				total := p.EndTime.Sub(p.StartTime).Seconds()
				elapsed := now.Sub(p.StartTime).Seconds()
				if total > 0 {
					pct := int((elapsed / total) * 100)
					return p.Title, pct
				}
				return p.Title, 0
			}
		}
	}

	return "", 0
}

// ForceRefresh 强制刷新
func (s *EPGService) ForceRefresh() error {
	lastFetchTime = time.Time{} // 重置时间
	s.FetchAndBuildIndex()
	return nil
}
