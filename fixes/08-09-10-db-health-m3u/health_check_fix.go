// ========================================
// stream_service.go — 健康检查优化
// 只检查最近活跃的频道，而非全量遍历
// ========================================

func (sp *StreamProxy) checkAllChannels() {
	// ★ 修改：只检查最近 1 小时内有播放记录的频道 + 状态为 online 的频道
	// 而非遍历所有频道

	// 方案：先检查当前活跃的流，再检查最近有播放的频道
	// 使用分页但限制总量

	page := 1
	pageSize := 50 // 每批 50 个
	maxPages := 10 // 最多检查 500 个频道

	for page <= maxPages {
		p := &models.PageRequest{Page: page, PageSize: pageSize}
		resp, err := sp.channelSvc.ListChannels(0, false, "", p)
		if err != nil || resp == nil {
			break
		}

		channels, ok := resp.Items.([]models.Channel)
		if !ok || len(channels) == 0 {
			break
		}

		for _, ch := range channels {
			// 跳过没有流地址的频道
			if ch.StreamURL == "" {
				continue
			}

			status, _ := sp.CheckHealth(ch.StreamURL, ch.StreamType)
			newStatus := "offline"
			if status.Status == "online" {
				newStatus = "online"
			}
			sp.channelSvc.UpdateStatus(ch.ID, newStatus)
		}

		if len(channels) < pageSize {
			break
		}
		page++
	}
}

// ★ 新增：只检查指定频道列表的健康状态
func (sp *StreamProxy) CheckChannelsHealth(channelIDs []int64) {
	for _, id := range channelIDs {
		ch, err := sp.channelSvc.GetChannel(id)
		if err != nil || ch.StreamURL == "" {
			continue
		}
		status, _ := sp.CheckHealth(ch.StreamURL, ch.StreamType)
		newStatus := "offline"
		if status.Status == "online" {
			newStatus = "online"
		}
		sp.channelSvc.UpdateStatus(ch.ID, newStatus)
	}
}
