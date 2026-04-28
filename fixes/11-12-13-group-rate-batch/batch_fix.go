// ========================================
// client_service.go — 批量操作使用事务
// ========================================

// Batch 批量操作（事务版）
func (s *ClientService) Batch(req *models.ClientBatchReq, approver string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("开启事务失败: %w", err)
	}
	defer tx.Rollback()

	count := 0
	now := time.Now()

	for _, id := range req.IDs {
		var err error
		switch req.Action {
		case "approve":
			token := generateToken()
			_, err = tx.Exec(`UPDATE clients SET status='approved', access_token=?, max_streams=?, expires_at=?, approved_by=?, reject_reason='', updated_at=? WHERE id=?`,
				token, 2, now.AddDate(0, 0, 365), approver, now, id)
		case "reject":
			_, err = tx.Exec(`UPDATE clients SET status='rejected', reject_reason=?, access_token='', updated_at=? WHERE id=?`,
				"批量拒绝", now, id)
		case "ban":
			_, err = tx.Exec(`UPDATE clients SET status='banned', reject_reason=?, access_token='', updated_at=? WHERE id=?`,
				"批量封禁", now, id)
		case "delete":
			_, err = tx.Exec(`DELETE FROM clients WHERE id=?`, id)
		default:
			return 0, fmt.Errorf("未知操作: %s", req.Action)
		}

		if err == nil {
			count++
			// 记录日志
			tx.Exec(`INSERT INTO access_logs (client_id, action, ip, detail, created_at) VALUES (?,?,?,?,?)`,
				id, req.Action, "", fmt.Sprintf("批量%s", req.Action), now)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %w", err)
	}

	return count, nil
}
