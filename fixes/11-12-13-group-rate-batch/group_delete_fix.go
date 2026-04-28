// ========================================
// channel_service.go — 分组删除安全处理
// ========================================

// DeleteGroup 删除分组时，将关联的频道移到"未分类"分组
func (s *ChannelService) DeleteGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 获取或创建"未分类"分组
	var defaultGroupID int64
	err = tx.QueryRow("SELECT id FROM channel_groups WHERE name = '未分类'").Scan(&defaultGroupID)
	if err != nil {
		// "未分类"分组不存在，创建一个
		res, err := tx.Exec("INSERT INTO channel_groups (name, sort_order) VALUES ('未分类', 99)")
		if err != nil {
			return err
		}
		defaultGroupID, _ = res.LastInsertId()
	}

	// 将该分组下的频道移到"未分类"
	_, err = tx.Exec("UPDATE channels SET group_id = ?, updated_at = ? WHERE group_id = ?",
		defaultGroupID, time.Now(), id)
	if err != nil {
		return err
	}

	// 删除分组
	_, err = tx.Exec("DELETE FROM channel_groups WHERE id = ?", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}
