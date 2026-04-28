package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

type ClientService struct {
	db *sql.DB
}

func NewClientService(db *sql.DB) *ClientService {
	return &ClientService{db: db}
}

// ── Token 生成 ─────────────────────────────────────────

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ── 客户端注册 ─────────────────────────────────────────

func (s *ClientService) Register(req *models.ClientRegisterReq, ip string) (*models.ClientRegisterResp, error) {
	now := time.Now()

	// 检查是否已注册
	var existing models.Client
	err := s.db.QueryRow(`SELECT id, status, access_token, expires_at FROM clients WHERE device_id=?`, req.DeviceID).
		Scan(&existing.ID, &existing.Status, &existing.AccessToken, &existing.ExpiresAt)

	if err == nil {
		// 已注册，更新信息
		s.db.Exec(`UPDATE clients SET name=?, device_model=?, device_os=?, app_version=?, ip=?, last_seen=?, updated_at=? WHERE id=?`,
			req.Name, req.DeviceModel, req.DeviceOS, req.AppVersion, ip, now, now, existing.ID)

		resp := &models.ClientRegisterResp{
			ClientID: existing.ID,
			Status:   existing.Status,
			Message:  statusMessage(existing.Status),
		}

		if existing.Status == "approved" && existing.AccessToken != "" {
			resp.AccessToken = existing.AccessToken
			if !existing.ExpiresAt.IsZero() {
				resp.ExpiresAt = existing.ExpiresAt.Format(time.RFC3339)
			}
		}
		return resp, nil
	}

	// 新注册
	token := generateToken()

	// 检查自动审批设置
	autoApprove := false
	if val, err := s.GetSettingValue("auto_approve"); err == nil && val == "true" {
		autoApprove = true
	}

	status := "pending"
	var expiresAt *time.Time
	if autoApprove {
		status = "approved"
		days := 365
		if val, err := s.GetSettingValue("default_expire_days"); err == nil {
			fmt.Sscanf(val, "%d", &days)
		}
		t := now.AddDate(0, 0, days)
		expiresAt = &t
	}

	maxStreams := 2
	if val, err := s.GetSettingValue("default_max_streams"); err == nil {
		fmt.Sscanf(val, "%d", &maxStreams)
	}

	res, err := s.db.Exec(`INSERT INTO clients (name, device_id, device_model, device_os, app_version, ip, access_token, status, max_streams, expires_at, last_seen, request_note, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		req.Name, req.DeviceID, req.DeviceModel, req.DeviceOS, req.AppVersion, ip, token, status, maxStreams, expiresAt, now, req.Note, now, now)
	if err != nil {
		return nil, fmt.Errorf("注册失败: %w", err)
	}

	clientID, _ := res.LastInsertId()

	// 记录日志
	s.AddLog(clientID, "register", 0, ip, "", "新设备注册")

	resp := &models.ClientRegisterResp{
		ClientID: clientID,
		Status:   status,
		Message:  statusMessage(status),
	}

	if status == "approved" {
		resp.AccessToken = token
		if expiresAt != nil {
			resp.ExpiresAt = expiresAt.Format(time.RFC3339)
		}
	}

	return resp, nil
}

// ── 客户端验证 ─────────────────────────────────────────

func (s *ClientService) Validate(token, ip string) (*models.Client, error) {
	var c models.Client
	err := s.db.QueryRow(`SELECT id, name, device_id, device_model, status, max_streams, expires_at, access_token FROM clients WHERE access_token=?`, token).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.Status, &c.MaxStreams, &c.ExpiresAt, &c.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("无效的令牌")
	}

	// 检查状态
	if c.Status != "approved" {
		return nil, fmt.Errorf("客户端未授权: %s", c.Status)
	}

	// 检查过期
	if !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(time.Now()) {
		s.db.Exec(`UPDATE clients SET status='expired', updated_at=? WHERE id=?`, time.Now(), c.ID)
		return nil, fmt.Errorf("授权已过期")
	}

	// 更新最后在线
	s.db.Exec(`UPDATE clients SET last_seen=?, ip=? WHERE id=?`, time.Now(), ip, c.ID)

	return &c, nil
}

// ── 审批 ───────────────────────────────────────────────

func (s *ClientService) Approve(clientID int64, req *models.ClientApproveReq, approver string) error {
	now := time.Now()
	token := generateToken()

	maxStreams := req.MaxStreams
	if maxStreams <= 0 {
		maxStreams = 2
	}

	var expiresAt *time.Time
	if req.MaxDays > 0 {
		t := now.AddDate(0, 0, req.MaxDays)
		expiresAt = &t
	}

	_, err := s.db.Exec(`UPDATE clients SET status='approved', access_token=?, max_streams=?, expires_at=?, approved_by=?, reject_reason='', updated_at=? WHERE id=?`,
		token, maxStreams, expiresAt, approver, now, clientID)
	if err != nil {
		return err
	}

	s.AddLog(clientID, "approved", 0, "", "", fmt.Sprintf("由 %s 审批通过", approver))
	return nil
}

// ── 拒绝 ───────────────────────────────────────────────

func (s *ClientService) Reject(clientID int64, req *models.ClientRejectReq) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE clients SET status='rejected', reject_reason=?, access_token='', updated_at=? WHERE id=?`,
		req.Reason, now, clientID)
	if err != nil {
		return err
	}

	s.AddLog(clientID, "rejected", 0, "", "", req.Reason)
	return nil
}

// ── 封禁 ───────────────────────────────────────────────

func (s *ClientService) Ban(clientID int64, reason string) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE clients SET status='banned', reject_reason=?, access_token='', updated_at=? WHERE id=?`,
		reason, now, clientID)
	if err != nil {
		return err
	}

	s.AddLog(clientID, "banned", 0, "", "", reason)
	return nil
}

// ── 解封 ───────────────────────────────────────────────

func (s *ClientService) Unban(clientID int64) error {
	now := time.Now()
	token := generateToken()
	_, err := s.db.Exec(`UPDATE clients SET status='pending', access_token=?, reject_reason='', updated_at=? WHERE id=?`,
		token, now, clientID)
	return err
}

// ── 吊销令牌 ───────────────────────────────────────────

func (s *ClientService) RevokeToken(clientID int64) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE clients SET access_token='', updated_at=? WHERE id=?`, now, clientID)
	if err != nil {
		return err
	}

	s.AddLog(clientID, "token_revoked", 0, "", "", "令牌已吊销")
	return nil
}

// ── 重新生成令牌 ───────────────────────────────────────

func (s *ClientService) RegenerateToken(clientID int64) (string, error) {
	token := generateToken()
	now := time.Now()
	_, err := s.db.Exec(`UPDATE clients SET access_token=?, updated_at=? WHERE id=?`, token, now, clientID)
	if err != nil {
		return "", err
	}

	s.AddLog(clientID, "token_regenerated", 0, "", "", "令牌已重新生成")
	return token, nil
}

// ── 查询 ───────────────────────────────────────────────

func (s *ClientService) List(status string, search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()
	where := "WHERE 1=1"
	args := []interface{}{}

	if status != "" {
		where += " AND status=?"
		args = append(args, status)
	}
	if search != "" {
		where += " AND (name LIKE ? OR device_id LIKE ? OR device_model LIKE ? OR ip LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s, s)
	}

	var total int64
	if err := s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM clients %s", where), args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PageSize
	queryArgs := append(args, p.PageSize, offset)
	rows, err := s.db.Query(fmt.Sprintf(`SELECT id, name, device_id, device_model, device_os, app_version, ip, status, max_streams, expires_at, approved_by, reject_reason, last_seen, total_play_minutes, request_note, created_at, updated_at FROM clients %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, where), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var clients []models.Client
	for rows.Next() {
		var c models.Client
		if err := rows.Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.DeviceOS, &c.AppVersion, &c.IP, &c.Status, &c.MaxStreams, &c.ExpiresAt, &c.ApprovedBy, &c.RejectReason, &c.LastSeen, &c.TotalPlayMin, &c.RequestNote, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	resp := &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: clients}
	if items, ok := resp.Items.([]models.Client); ok {
		for i := range items {
			if len(items[i].AccessToken) > 8 {
				items[i].TokenPreview = items[i].AccessToken[:8] + "..."
			}
		}
	}
	return resp, nil
}

func (s *ClientService) GetByID(id int64) (*models.Client, error) {
	var c models.Client
	err := s.db.QueryRow(`SELECT id, name, device_id, device_model, device_os, app_version, ip, access_token, status, max_streams, expires_at, approved_by, reject_reason, last_seen, total_play_minutes, request_note, created_at, updated_at FROM clients WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.DeviceOS, &c.AppVersion, &c.IP, &c.AccessToken, &c.Status, &c.MaxStreams, &c.ExpiresAt, &c.ApprovedBy, &c.RejectReason, &c.LastSeen, &c.TotalPlayMin, &c.RequestNote, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// 生成预览
	if len(c.AccessToken) > 8 {
		c.TokenPreview = c.AccessToken[:8] + "..."
	}
	return &c, nil
}

func (s *ClientService) GetByToken(token string) (*models.Client, error) {
	var c models.Client
	err := s.db.QueryRow(`SELECT id, name, device_id, status FROM clients WHERE access_token=?`, token).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.Status)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *ClientService) Delete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM clients WHERE id=?`, id)
	return err
}

// ── 批量操作 ───────────────────────────────────────────

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
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("提交事务失败: %w", err)
	}

	return count, nil
}

// ── 播放时长统计 ───────────────────────────────────────

func (s *ClientService) AddPlayTime(clientID int64, minutes int) error {
	_, err := s.db.Exec(`UPDATE clients SET total_play_minutes = total_play_minutes + ?, last_seen=?, updated_at=? WHERE id=?`,
		minutes, time.Now(), time.Now(), clientID)
	return err
}

// ── 访问日志 ───────────────────────────────────────────

func (s *ClientService) AddLog(clientID int64, action string, channelID int64, ip, userAgent, detail string) {
	s.db.Exec(`INSERT INTO access_logs (client_id, action, channel_id, ip, user_agent, detail, created_at) VALUES (?,?,?,?,?,?,?)`,
		clientID, action, channelID, ip, userAgent, detail, time.Now())
}

func (s *ClientService) GetLogs(clientID int64, limit int) ([]models.AccessLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, client_id, action, channel_id, ip, user_agent, detail, created_at FROM access_logs WHERE client_id=? ORDER BY created_at DESC LIMIT ?`, clientID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.AccessLog
	for rows.Next() {
		var l models.AccessLog
		if err := rows.Scan(&l.ID, &l.ClientID, &l.Action, &l.ChannelID, &l.IP, &l.UserAgent, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (s *ClientService) GetRecentLogs(limit int) ([]models.AccessLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, client_id, action, channel_id, ip, user_agent, detail, created_at FROM access_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []models.AccessLog
	for rows.Next() {
		var l models.AccessLog
		if err := rows.Scan(&l.ID, &l.ClientID, &l.Action, &l.ChannelID, &l.IP, &l.UserAgent, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

// ── 统计 ───────────────────────────────────────────────

func (s *ClientService) GetClientStats() (total, pending, online int) {
	s.db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&total)
	s.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE status='pending'`).Scan(&pending)
	// 5分钟内活跃
	s.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE last_seen > datetime('now', '-5 minutes')`).Scan(&online)
	return
}

// ── 过期清理 ───────────────────────────────────────────

func (s *ClientService) ExpireOldClients() (int, error) {
	res, err := s.db.Exec(`UPDATE clients SET status='expired', updated_at=? WHERE status='approved' AND expires_at IS NOT NULL AND expires_at < ?`,
		time.Now(), time.Now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ── 辅助 ───────────────────────────────────────────────

func (s *ClientService) GetSettingValue(key string) (string, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key=?`, key).Scan(&val)
	return val, err
}

func statusMessage(status string) string {
	switch status {
	case "approved":
		return "已授权，可以使用"
	case "pending":
		return "已提交申请，等待管理员审批"
	case "rejected":
		return "申请被拒绝"
	case "banned":
		return "设备已被封禁"
	case "expired":
		return "授权已过期"
	default:
		return "未知状态"
	}
}
