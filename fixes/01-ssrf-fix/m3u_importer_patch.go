// ========================================
// m3u_importer.go — 需要修改的部分
// ========================================

// ── 修改 ImportFromURL 方法，添加 URL 校验 ──

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

	// ★ 新增：校验 M3U 源 URL
	if err := ValidateStreamURL(source.URL); err != nil {
		return 0, fmt.Errorf("M3U 源地址不安全: %w", err)
	}

	resp, err := http.Get(source.URL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	// ★ 新增：检查 HTTP 响应状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return 0, fmt.Errorf("M3U 源返回错误状态码: %d", resp.StatusCode)
	}

	channels, err := ParseM3U(resp.Body)
	if err != nil {
		return 0, err
	}

	count, err := imp.importChannels(channels)
	if err == nil {
		imp.channelSvc.db.Exec("UPDATE m3u_sources SET last_sync=? WHERE id=?", time.Now(), sourceID)
	}
	return count, err
}
