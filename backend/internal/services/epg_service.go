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

	"github.com/mediaplayer/backend/internal/models"
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
	mu            sync.RWMutex
	programs      map[string]map[string][]models.EPGProgram // channelIDLower -> date("2006-01-02") -> programs
	lastFetchTime time.Time
	lastFetchDate string // "2006-01-02"
	isFetching    bool
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

type parsedEPGResult struct {
	sourceURL    string
	srcIdx       int
	newIndex     map[string]map[string][]models.EPGProgram
	displayNames map[string][]string
	addedCount   int
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

func (s *EPGService) FetchAndBuildIndex() {
	var sourceURLRaw string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_source_url'`).Scan(&sourceURLRaw)
	if err != nil || sourceURLRaw == "" {
		return // 未配置 EPG
	}

	rawUrls := strings.Split(strings.ReplaceAll(sourceURLRaw, "\r", "\n"), "\n")
	var urls []string
	for _, u := range rawUrls {
		u = strings.TrimSpace(u)
		if u != "" {
			urls = append(urls, u)
		}
	}
	if len(urls) == 0 {
		return
	}

	refreshHours := 12
	var refreshHoursStr string
	if err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_refresh_hours'`).Scan(&refreshHoursStr); err == nil && refreshHoursStr != "" {
		_, _ = fmt.Sscanf(refreshHoursStr, "%d", &refreshHours)
	}
	if refreshHours <= 0 {
		refreshHours = 12
	}

	// 检查是否需要刷新
	globalEPGIndex.mu.RLock()
	lastTime := globalEPGIndex.lastFetchTime
	lastDate := globalEPGIndex.lastFetchDate
	isFetching := globalEPGIndex.isFetching
	globalEPGIndex.mu.RUnlock()

	// 如果当前正在拉取，直接返回避免并发
	if isFetching {
		return
	}

	now := time.Now()
	nowDate := now.Format("2006-01-02")
	isCrossDay := !lastTime.IsZero() && nowDate != lastDate

	// 触发刷新条件：跨天，或者距离上次刷新超过设定的小时数
	if !isCrossDay && time.Since(lastTime) < time.Duration(refreshHours)*time.Hour {
		return
	}

	// 标记开始拉取
	globalEPGIndex.mu.Lock()
	globalEPGIndex.isFetching = true
	globalEPGIndex.mu.Unlock()

	// 无论成功失败，保证拉取结束后重置状态，并捕获可能的 panic 保证后台任务存活
	defer func() {
		if r := recover(); r != nil {
			slog.Error("EPG 抓取过程发生异常 (Panic)", "panic", r)
		}
		globalEPGIndex.mu.Lock()
		globalEPGIndex.isFetching = false
		globalEPGIndex.mu.Unlock()
	}()

	slog.Info("开始拉取 EPG 数据", "urls_count", len(urls))

	// 获取所有需要的频道，用于过滤，极大降低内存消耗
	neededChannels := make(map[string]bool)
	rows, dbErr := s.db.Query("SELECT epg_channel_id, name FROM channels")
	if dbErr == nil {
		defer rows.Close()
		for rows.Next() {
			var epgID, name string
			if rows.Scan(&epgID, &name) == nil {
				if epgID != "" {
					neededChannels[strings.ToLower(epgID)] = true
					neededChannels[normalizeChannelName(epgID)] = true
				}
				if name != "" {
					neededChannels[strings.ToLower(name)] = true
					neededChannels[normalizeChannelName(name)] = true
				}
			}
		}
	} else {
		slog.Error("获取本地频道列表失败", "error", dbErr)
	}

	// EPG 清理旧数据（7天前的），因为我们现在是就地更新而不是完全替换 map
	sevenDaysAgoStr := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
	globalEPGIndex.mu.Lock()
	for chID, dateMap := range globalEPGIndex.programs {
		for date := range dateMap {
			if date < sevenDaysAgoStr {
				delete(dateMap, date)
			}
		}
		if len(dateMap) == 0 {
			delete(globalEPGIndex.programs, chID)
		}
	}
	globalEPGIndex.mu.Unlock()

	// 记录本次更新周期内，各频道的提供者优先级，防止低优先级覆盖高优先级
	cycleChannelSourceIdx := make(map[string]int)

	client := &http.Client{Timeout: 60 * time.Second}
	results := make(chan *parsedEPGResult, len(urls))
	var wg sync.WaitGroup

	for i, u := range urls {
		wg.Add(1)
		go func(srcIdx int, sourceURL string) {
			defer wg.Done()
			slog.Info("正在后台拉取 EPG 源", "url", sourceURL, "priority", srcIdx)

			resp, err := client.Get(sourceURL)
			if err != nil {
				slog.Error("EPG 拉取失败", "url", sourceURL, "error", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				slog.Error("EPG 拉取失败", "url", sourceURL, "status", resp.StatusCode)
				return
			}

			var reader io.Reader = resp.Body
			isGz := strings.HasSuffix(strings.ToLower(sourceURL), ".gz") || strings.Contains(resp.Header.Get("Content-Encoding"), "gzip")
			if isGz {
				gr, err := gzip.NewReader(resp.Body)
				if err != nil {
					slog.Error("EPG Gzip 解压失败", "url", sourceURL, "error", err)
					return
				}
				defer gr.Close()
				reader = gr
			}

			decoder := xml.NewDecoder(reader)
			var tv xmltvTV
			if err := decoder.Decode(&tv); err != nil {
				slog.Error("EPG XML 解析失败", "url", sourceURL, "error", err)
				return
			}

			neededXMLIDs := make(map[string]bool)
			localDisplayNames := make(map[string][]string)

			for _, ch := range tv.Channels {
				chIDLower := strings.ToLower(ch.ID)
				localDisplayNames[chIDLower] = append(localDisplayNames[chIDLower], ch.DisplayName...)

				isNeeded := false
				if neededChannels[chIDLower] || neededChannels[normalizeChannelName(chIDLower)] {
					isNeeded = true
				} else {
					for _, dn := range ch.DisplayName {
						dnLower := strings.ToLower(dn)
						if neededChannels[dnLower] || neededChannels[normalizeChannelName(dnLower)] {
							isNeeded = true
							break
						}
					}
				}
				if isNeeded {
					neededXMLIDs[chIDLower] = true
				}
			}

			localIndex := make(map[string]map[string][]models.EPGProgram)
			addedCount := 0

			for _, prog := range tv.Programmes {
				chID := strings.ToLower(prog.Channel)
				if !neededXMLIDs[chID] {
					continue
				}

				start, err1 := parseXmltvTime(prog.Start)
				stop, err2 := parseXmltvTime(prog.Stop)
				if err1 != nil || err2 != nil {
					continue
				}

				dateKey := start.In(time.Local).Format("2006-01-02")
				if dateKey < sevenDaysAgoStr {
					continue
				}

				if _, ok := localIndex[chID]; !ok {
					localIndex[chID] = make(map[string][]models.EPGProgram)
				}

				p := models.EPGProgram{
					ChannelID: chID,
					Title:     prog.Title.Value,
					StartTime: start,
					EndTime:   stop,
					Desc:      prog.Desc.Value,
				}
				localIndex[chID][dateKey] = append(localIndex[chID][dateKey], p)
				addedCount++
			}

			slog.Info("EPG 源下载及解析完成，准备合入内存", "url", sourceURL, "added_programs", addedCount)

			results <- &parsedEPGResult{
				sourceURL:    sourceURL,
				srcIdx:       srcIdx,
				newIndex:     localIndex,
				displayNames: localDisplayNames,
				addedCount:   addedCount,
			}
		}(i, u)
	}

	// 启动一个协程等待所有任务结束，并关闭 results 通道
	go func() {
		wg.Wait()
		close(results)
	}()

	// 在主协程中持续接收结果，并逐步合并到全局内存中
	totalAdded := 0
	for res := range results {
		// 先对当前源的结果按时间排序
		for ch, dateMap := range res.newIndex {
			for date, progs := range dateMap {
				sort.Slice(progs, func(i, j int) bool {
					return progs[i].StartTime.Before(progs[j].StartTime)
				})
				res.newIndex[ch][date] = progs
			}
		}

		// 建立显示名称到频道的映射（模糊匹配用）
		for chID, displayNames := range res.displayNames {
			if progs, exists := res.newIndex[chID]; exists {
				for _, dn := range displayNames {
					dnLower := strings.ToLower(dn)
					if _, ok := res.newIndex[dnLower]; !ok {
						res.newIndex[dnLower] = progs
					}
				}
			}
		}

		// 加锁，将结果合入全局索引
		globalEPGIndex.mu.Lock()
		for chID, dateMap := range res.newIndex {
			// 检查优先级：如果是本轮更新中优先级更低的源（srcIdx 更大），且之前已经有高优先级源合入了数据，则跳过
			if prevIdx, exists := cycleChannelSourceIdx[chID]; exists && prevIdx < res.srcIdx {
				continue
			}
			// 记录此频道的最高提供者优先级
			cycleChannelSourceIdx[chID] = res.srcIdx

			if _, ok := globalEPGIndex.programs[chID]; !ok {
				globalEPGIndex.programs[chID] = make(map[string][]models.EPGProgram)
			}
			for date, progs := range dateMap {
				globalEPGIndex.programs[chID][date] = progs
			}
		}

		// 更新最后抓取时间
		globalEPGIndex.lastFetchTime = time.Now()
		globalEPGIndex.lastFetchDate = time.Now().Format("2006-01-02")
		globalEPGIndex.mu.Unlock()

		totalAdded += res.addedCount
		slog.Info("EPG 内存已渐进式更新", "url", res.sourceURL, "merged_programs", res.addedCount)
	}

	slog.Info("本轮 EPG 数据并发拉取更新结束", "total_merged_programs", totalAdded)

	// ── 自动补全 EPG 频道 ID ──
	go s.autoCompleteEPGChannelIDs()
}

// 异步检查并触发跨天刷新
func (s *EPGService) triggerCrossDayRefresh() {
	globalEPGIndex.mu.RLock()
	lastDate := globalEPGIndex.lastFetchDate
	lastTime := globalEPGIndex.lastFetchTime
	globalEPGIndex.mu.RUnlock()

	if !lastTime.IsZero() && time.Now().Format("2006-01-02") != lastDate {
		go s.FetchAndBuildIndex()
	}
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
	s.triggerCrossDayRefresh()

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
func (s *EPGService) GetCurrentEPGWithProgress(channelID string) (string, string, int) {
	s.triggerCrossDayRefresh()

	globalEPGIndex.mu.RLock()
	defer globalEPGIndex.mu.RUnlock()

	chID := strings.ToLower(channelID)
	chIDClean := normalizeChannelName(channelID)
	now := time.Now()

	findProgram := func(dateMap map[string][]models.EPGProgram) (string, string, int, bool) {
		for _, progs := range dateMap {
			for i, p := range progs {
				if !now.Before(p.StartTime) && now.Before(p.EndTime) {
					total := p.EndTime.Sub(p.StartTime).Seconds()
					elapsed := now.Sub(p.StartTime).Seconds()
					
					currentTitle := fmt.Sprintf("%s-%s %s", p.StartTime.Format("15:04"), p.EndTime.Format("15:04"), p.Title)
					
					nextTitle := ""
					if i+1 < len(progs) {
						nextP := progs[i+1]
						nextTitle = fmt.Sprintf("%s-%s %s", nextP.StartTime.Format("15:04"), nextP.EndTime.Format("15:04"), nextP.Title)
					}

					if total > 0 {
						pct := int((elapsed / total) * 100)
						return currentTitle, nextTitle, pct, true
					}
					return currentTitle, nextTitle, 0, true
				}
			}
		}
		return "", "", 0, false
	}

	// 1. 尝试精确匹配
	if dateMap, ok := globalEPGIndex.programs[chID]; ok {
		if title, nextTitle, pct, found := findProgram(dateMap); found {
			return title, nextTitle, pct
		}
	}

	for key, dateMap := range globalEPGIndex.programs {
		if key == chID {
			continue 
		}
		if normalizeChannelName(key) == chIDClean {
			if title, nextTitle, pct, found := findProgram(dateMap); found {
				return title, nextTitle, pct
			}
		}
	}

	return "", "", 0
}

// ForceRefresh 强制刷新
func (s *EPGService) ForceRefresh() error {
	globalEPGIndex.mu.Lock()
	globalEPGIndex.lastFetchTime = time.Time{} // 重置时间
	globalEPGIndex.mu.Unlock()
	s.FetchAndBuildIndex()
	return nil
}

// MatchEPGChannel 检查内存中是否存在该频道的任何 EPG 数据，如果存在，返回真正匹配的 epg_channel_id
func (s *EPGService) MatchEPGChannel(channelID string) (string, bool) {
	globalEPGIndex.mu.RLock()
	defer globalEPGIndex.mu.RUnlock()

	if channelID == "" {
		return "", false
	}

	chID := strings.ToLower(channelID)
	chIDClean := normalizeChannelName(channelID)

	// 精确匹配
	if _, ok := globalEPGIndex.programs[chID]; ok {
		return chID, true
	}

	// 模糊匹配
	for key := range globalEPGIndex.programs {
		if normalizeChannelName(key) == chIDClean {
			return key, true
		}
	}

	return "", false
}

// HasEPG 检查内存中是否存在该频道的任何 EPG 数据
func (s *EPGService) HasEPG(channelID string) bool {
	_, found := s.MatchEPGChannel(channelID)
	return found
}

// autoCompleteEPGChannelIDs 自动为 epg_channel_id 为空的频道补全匹配的频道名称
func (s *EPGService) autoCompleteEPGChannelIDs() {
	rows, err := s.db.Query(`SELECT id, name FROM channels WHERE epg_channel_id = '' OR epg_channel_id IS NULL`)
	if err != nil {
		slog.Error("自动补全 EPG 频道 ID 时查询失败", "error", err)
		return
	}
	defer rows.Close()

	updatedCount := 0
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}

		if name == "" {
			continue
		}

		if matchID, found := s.MatchEPGChannel(name); found {
			// 这里将真正匹配到的标准 EPG ID 写入数据库，而不是原始的 name
			_, err := s.db.Exec(`UPDATE channels SET epg_channel_id = ? WHERE id = ?`, matchID, id)
			if err == nil {
				updatedCount++
				slog.Info("自动补全 EPG 频道 ID 成功", "channel_id", id, "original_name", name, "matched_epg_id", matchID)
			}
		}
	}

	if updatedCount > 0 {
		slog.Info("本轮 EPG 自动补全结束", "updated_channels", updatedCount)
	}
}
