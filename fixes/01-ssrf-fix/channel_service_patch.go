// ========================================
// channel_service.go — 在 CreateChannel 和 UpdateChannel 中添加校验
// ========================================

// ── 修改 CreateChannel ──
func (s *ChannelService) CreateChannel(c *models.Channel) error {
	// ★ 新增：校验流地址
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO channels (group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, sort_order, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, c.IsFavorite, c.IsHidden, c.SortOrder, "unknown", now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = now
	c.UpdatedAt = now
	return nil
}

// ── 修改 UpdateChannel ──
func (s *ChannelService) UpdateChannel(c *models.Channel) error {
	// ★ 新增：校验流地址
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	_, err := s.db.Exec(`UPDATE channels SET group_id=?, name=?, logo=?, description=?, stream_url=?, stream_type=?, epg_channel_id=?, is_favorite=?, is_hidden=?, sort_order=?, updated_at=? WHERE id=?`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, c.IsFavorite, c.IsHidden, c.SortOrder, time.Now(), c.ID)
	return err
}
