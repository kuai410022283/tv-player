// ========================================
// stream_service.go — 需要修改的部分
// ========================================

// 在文件顶部 import 中添加:
//   "fmt" (已有)

// ── 修改 ServeStream 方法，在请求上游前添加 URL 校验 ──

// ServeStream proxies the actual stream data
func (sp *StreamProxy) ServeStream(channelID int64, w http.ResponseWriter, r *http.Request) error {
	// 并发控制
	select {
	case sp.sem <- struct{}{}:
		defer func() { <-sp.sem }()
	default:
		return fmt.Errorf("并发流数已达上限 (%d)", sp.cfg.MaxConcurrent)
	}

	ch, err := sp.channelSvc.GetChannel(channelID)
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	// ★ 新增：校验流地址，防止 SSRF
	if err := ValidateStreamURL(ch.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	// Update stream state
	sp.mu.Lock()
	sp.streams[channelID] = &streamState{
		ChannelID:  channelID,
		URL:        ch.StreamURL,
		Status:     "playing",
		StartedAt:  time.Now(),
		LastActive: time.Now(),
	}
	sp.mu.Unlock()

	defer func() {
		sp.mu.Lock()
		delete(sp.streams, channelID)
		sp.mu.Unlock()
	}()

	// Proxy the stream
	req, err := http.NewRequestWithContext(r.Context(), "GET", ch.StreamURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TVPlayer/1.0")

	resp, err := sp.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Copy headers
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream with buffering
	buf := make([]byte, sp.cfg.BufferSize)
	reader := bufio.NewReaderSize(resp.Body, sp.cfg.BufferSize)

	for {
		n, err := reader.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			sp.mu.Lock()
			if s, ok := sp.streams[channelID]; ok {
				s.LastActive = time.Now()
			}
			sp.mu.Unlock()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// ── 修改 CheckHealth 方法，添加 URL 校验 ──

// CheckHealth verifies a stream URL is reachable and returns stream info
func (sp *StreamProxy) CheckHealth(url, streamType string) (*models.StreamStatus, error) {
	status := &models.StreamStatus{
		URL:    url,
		Status: "unknown",
	}

	// ★ 新增：校验 URL
	if err := ValidateStreamURL(url); err != nil {
		status.Status = "error"
		status.ErrorMsg = "URL 不安全: " + err.Error()
		return status, err
	}

	// 健康检查用独立短超时 client
	healthClient := &http.Client{Timeout: 10 * time.Second}

	switch streamType {
	case "hls", "mp4", "dash", "flv":
		resp, err := healthClient.Head(url)
		if err != nil {
			status.Status = "error"
			status.ErrorMsg = err.Error()
			return status, err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			status.Status = "online"
		} else {
			status.Status = "offline"
			status.ErrorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	case "rtmp", "rtsp":
		status.Status = "online"
	default:
		status.Status = "unknown"
	}
	return status, nil
}
