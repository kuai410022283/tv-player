package services

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

type ClientService struct {
	db *sql.DB
}

func NewClientService(db *sql.DB) *ClientService {
	// 自动修复由于超大天数计算导致的时间格式损坏，防止数据库读取崩溃
	_, _ = db.Exec(`UPDATE clients SET expires_at = '9999-12-31 23:59:59' WHERE instr(expires_at, '-') > 5`)
	return &ClientService{db: db}
}

func capExpiration(t time.Time) time.Time {
	maxTime := time.Date(9999, 12, 31, 23, 59, 59, 0, t.Location())
	if t.After(maxTime) {
		return maxTime
	}
	return t
}

// ── Token 生成 ─────────────────────────────────────────

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ── 客户端注册 ─────────────────────────────────────────

func (s *ClientService) Register(req *models.ClientRegisterReq, ip string) (*models.ClientRegisterResp, error) {
	now := time.Now()

	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("生成令牌失败: %w", err)
	}

	// 检查自动审批设置
	autoApprove := false
	if val, err := s.GetSettingValue("auto_approve"); err == nil && val == "true" {
		autoApprove = true
	}

	status := "pending"
	var expiresAt *time.Time
	var planID int64

	maxStreams := 2
	if val, err := s.GetSettingValue("default_max_streams"); err == nil {
		_, _ = fmt.Sscanf(val, "%d", &maxStreams)
	}

	if autoApprove {
		status = "approved"

		// 检查是否配置了默认套餐
		if val, err := s.GetSettingValue("default_plan_id"); err == nil && val != "" && val != "0" {
			var pid int64
			_, _ = fmt.Sscanf(val, "%d", &pid)
			if pid > 0 {
				var days, pStreams int
				if err := s.db.QueryRow(`SELECT days, max_streams FROM subscription_plans WHERE id=?`, pid).Scan(&days, &pStreams); err == nil {
					planID = pid
					maxStreams = pStreams
					if days > 0 {
						t := capExpiration(now.AddDate(0, 0, days))
						expiresAt = &t
					}
				}
			}
		}

		// 若未匹配到套餐，则使用默认天数
		if planID == 0 {
			days := 365
			if val, err := s.GetSettingValue("default_expire_days"); err == nil {
				_, _ = fmt.Sscanf(val, "%d", &days)
			}
			if days > 0 {
				t := capExpiration(now.AddDate(0, 0, days))
				expiresAt = &t
			}
		}
	}

	// 先尝试 INSERT：利用 device_id UNIQUE 约束防止竞态条件
	res, err := s.db.Exec(`INSERT INTO clients (name, device_id, device_model, device_os, app_version, ip, access_token, status, max_streams, expires_at, last_seen, request_note, created_at, updated_at, plan_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		req.Name, req.DeviceID, req.DeviceModel, req.DeviceOS, req.AppVersion, ip, token, status, maxStreams, expiresAt, now, req.Note, now, now, planID)
	if err == nil {
		// INSERT 成功，设备不存在
		clientID, _ := res.LastInsertId()
		s.AddLog(clientID, "register", 0, ip, "", "新设备注册")

		resp := &models.ClientRegisterResp{
			ClientID:  clientID,
			Status:    status,
			Message:   statusMessage(status),
			EnableLog: false,
		}
		if status == "approved" {
			resp.AccessToken = token
			if expiresAt != nil {
				resp.ExpiresAt = expiresAt.Format(time.RFC3339)
			}
		}
		return resp, nil
	}

	// INSERT 失败（UNIQUE 约束冲突），说明设备已存在，降级为 UPDATE
	var existing models.Client
	var nullExpiresAt sql.NullTime
	err = s.db.QueryRow(`SELECT id, status, access_token, expires_at, enable_log, is_tester FROM clients WHERE device_id=?`, req.DeviceID).
		Scan(&existing.ID, &existing.Status, &existing.AccessToken, &nullExpiresAt, &existing.EnableLog, &existing.IsTester)
	if err != nil {
		return nil, fmt.Errorf("查询设备失败: %w", err)
	}
	if nullExpiresAt.Valid {
		existing.ExpiresAt = nullExpiresAt.Time
	}

	// 更新设备信息
	_, _ = s.db.Exec(`UPDATE clients SET name=?, device_model=?, device_os=?, app_version=?, ip=?, last_seen=?, request_note=?, updated_at=? WHERE id=?`,
		req.Name, req.DeviceModel, req.DeviceOS, req.AppVersion, ip, now, req.Note, now, existing.ID)

	resp := &models.ClientRegisterResp{
		ClientID:  existing.ID,
		Status:    existing.Status,
		Message:   statusMessage(existing.Status),
		EnableLog: existing.EnableLog,
		IsTester:  existing.IsTester,
	}

	if existing.Status == "approved" && existing.AccessToken != "" {
		resp.AccessToken = existing.AccessToken
		if !existing.ExpiresAt.IsZero() {
			resp.ExpiresAt = existing.ExpiresAt.Format(time.RFC3339)
		}
	}
	return resp, nil
}

// ── 客户端验证 ─────────────────────────────────────────

func (s *ClientService) Validate(token, ip string) (*models.Client, error) {
	var c models.Client
	var expiresAt sql.NullTime
	err := s.db.QueryRow(`SELECT c.id, c.name, c.device_id, c.device_model, c.status, c.max_streams, c.expires_at, c.access_token, c.enable_log, c.is_tester, COALESCE(p.name, '') FROM clients c LEFT JOIN subscription_plans p ON c.plan_id = p.id WHERE c.access_token=?`, token).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.Status, &c.MaxStreams, &expiresAt, &c.AccessToken, &c.EnableLog, &c.IsTester, &c.PlanName)
	if expiresAt.Valid {
		c.ExpiresAt = expiresAt.Time
	}
	if err != nil {
		return nil, fmt.Errorf("无效的令牌")
	}

	// 检查状态
	if c.Status != "approved" {
		return nil, fmt.Errorf("客户端未授权: %s", c.Status)
	}

	// 检查过期
	if !c.ExpiresAt.IsZero() && c.ExpiresAt.Before(time.Now()) {
		_, _ = s.db.Exec(`UPDATE clients SET status='expired', updated_at=? WHERE id=?`, time.Now(), c.ID)
		return nil, fmt.Errorf("授权已过期")
	}

	// 更新最后在线
	_, _ = s.db.Exec(`UPDATE clients SET last_seen=?, ip=? WHERE id=?`, time.Now(), ip, c.ID)

	return &c, nil
}

// CountActiveDevices 统计指定套餐下 30 分钟内活跃的设备数（排除当前设备）。
// 活跃定义：last_seen 在 30 分钟内，表明客户端心跳仍在运行。
func (s *ClientService) CountActiveDevices(planID int64, excludeClientID int64) (int, error) {
	var count int
	threshold := time.Now().Add(-30 * time.Minute)
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM clients WHERE plan_id=? AND status='approved' AND last_seen > ? AND id != ?`,
		planID, threshold, excludeClientID,
	).Scan(&count)
	return count, err
}

// ── 审批 ───────────────────────────────────────────────

func (s *ClientService) Approve(clientID int64, req *models.ClientApproveReq, approver string) error {
	now := time.Now()

	var currentToken string
	var currentStatus string
	_ = s.db.QueryRow("SELECT access_token, status FROM clients WHERE id=?", clientID).Scan(&currentToken, &currentStatus)

	token := currentToken
	if currentStatus != "approved" || token == "" {
		t, err := generateToken()
		if err != nil {
			return fmt.Errorf("生成令牌失败: %w", err)
		}
		token = t
	}

	maxStreams := req.MaxStreams
	var expiresAt *time.Time
	planID := req.PlanID

	// 若选择了套餐，获取套餐详情
	if planID > 0 {
		var days, pStreams int
		err := s.db.QueryRow(`SELECT days, max_streams FROM subscription_plans WHERE id=?`, planID).Scan(&days, &pStreams)
		if err == nil {
			maxStreams = pStreams
			if days > 0 {
				t := capExpiration(now.AddDate(0, 0, days))
				expiresAt = &t
			}
		} else {
			planID = 0 // 退回默认
		}
	} else {
		if req.MaxDays > 0 {
			t := capExpiration(now.AddDate(0, 0, req.MaxDays))
			expiresAt = &t
		}
	}

	if maxStreams <= 0 {
		maxStreams = 2
	}

	_, err := s.db.Exec(`UPDATE clients SET status='approved', access_token=?, max_streams=?, expires_at=?, approved_by=?, reject_reason='', updated_at=?, plan_id=? WHERE id=?`,
		token, maxStreams, expiresAt, approver, now, planID, clientID)
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
	token, err := generateToken()
	if err != nil {
		return fmt.Errorf("生成令牌失败: %w", err)
	}
	_, err = s.db.Exec(`UPDATE clients SET status='pending', access_token=?, reject_reason='', updated_at=? WHERE id=?`,
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
	token, err := generateToken()
	if err != nil {
		return "", fmt.Errorf("生成令牌失败: %w", err)
	}
	now := time.Now()
	_, err = s.db.Exec(`UPDATE clients SET access_token=?, updated_at=? WHERE id=?`, token, now, clientID)
	if err != nil {
		return "", err
	}

	s.AddLog(clientID, "token_regenerated", 0, "", "", "令牌已重新生成")
	return token, nil
}

// ── 日志配置管理 ───────────────────────────────────────

func (s *ClientService) UpdateLogConfig(clientID int64, enableLog bool) error {
	_, err := s.db.Exec(`UPDATE clients SET enable_log=?, updated_at=? WHERE id=?`, enableLog, time.Now(), clientID)
	if err != nil {
		return err
	}
	state := "开启"
	if !enableLog {
		state = "关闭"
	}
	s.AddLog(clientID, "log_config_updated", 0, "", "", "管理员"+state+"了远程日志采集")
	return nil
}

func (s *ClientService) SetTester(clientID int64, isTester bool) error {
	_, err := s.db.Exec(`UPDATE clients SET is_tester=?, updated_at=? WHERE id=?`, isTester, time.Now(), clientID)
	if err != nil {
		return err
	}
	state := "设为测试机"
	if !isTester {
		state = "取消测试机"
	}
	s.AddLog(clientID, "tester_updated", 0, "", "", "管理员将其"+state)
	return nil
}

// ── 查询 ───────────────────────────────────────────────

func (s *ClientService) List(status string, search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()
	where := "WHERE 1=1"     // 用于 COUNT 查询（无表别名）
	mainWhere := "WHERE 1=1" // 用于主查询（使用 c. 前缀避免 JOIN 歧义）
	args := []interface{}{}

	if status != "" {
		where += " AND status=?"
		mainWhere += " AND c.status=?"
		args = append(args, status)
	}
	if search != "" {
		where += " AND (name LIKE ? OR device_id LIKE ? OR device_model LIKE ? OR ip LIKE ?)"
		mainWhere += " AND (c.name LIKE ? OR c.device_id LIKE ? OR c.device_model LIKE ? OR c.ip LIKE ?)"
		s := "%" + search + "%"
		args = append(args, s, s, s, s)
	}

	var total int64
	if err := s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM clients %s", where), args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PageSize
	queryArgs := append(args, p.PageSize, offset)
	rows, err := s.db.Query(fmt.Sprintf(`SELECT c.id, c.name, c.device_id, c.device_model, c.device_os, c.app_version, c.ip, c.status, c.plan_id, c.max_streams, c.expires_at, c.approved_by, c.reject_reason, c.last_seen, c.total_play_minutes, c.request_note, c.enable_log, c.is_tester, c.created_at, c.updated_at, COALESCE(p.name, '') as plan_name FROM clients c LEFT JOIN subscription_plans p ON c.plan_id = p.id %s ORDER BY c.created_at DESC LIMIT ? OFFSET ?`, mainWhere), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var clients []models.Client
	for rows.Next() {
		var c models.Client
		var expiresAt, lastSeen sql.NullTime
		var approvedBy, rejectReason, reqNote sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.DeviceOS, &c.AppVersion, &c.IP, &c.Status, &c.PlanID, &c.MaxStreams, &expiresAt, &approvedBy, &rejectReason, &lastSeen, &c.TotalPlayMin, &reqNote, &c.EnableLog, &c.IsTester, &c.CreatedAt, &c.UpdatedAt, &c.PlanName); err != nil {
			return nil, err
		}
		if expiresAt.Valid {
			c.ExpiresAt = expiresAt.Time
		}
		if lastSeen.Valid {
			c.LastSeen = lastSeen.Time
		}
		if approvedBy.Valid {
			c.ApprovedBy = approvedBy.String
		}
		if rejectReason.Valid {
			c.RejectReason = rejectReason.String
		}
		if reqNote.Valid {
			c.RequestNote = reqNote.String
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
	var expiresAt, lastSeen sql.NullTime
	var approvedBy, rejectReason, reqNote sql.NullString
	err := s.db.QueryRow(`SELECT c.id, c.name, c.device_id, c.device_model, c.device_os, c.app_version, c.ip, c.access_token, c.status, c.plan_id, c.max_streams, c.expires_at, c.approved_by, c.reject_reason, c.last_seen, c.total_play_minutes, c.request_note, c.enable_log, c.is_tester, c.created_at, c.updated_at, COALESCE(p.name, '') as plan_name FROM clients c LEFT JOIN subscription_plans p ON c.plan_id = p.id WHERE c.id=?`, id).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.DeviceModel, &c.DeviceOS, &c.AppVersion, &c.IP, &c.AccessToken, &c.Status, &c.PlanID, &c.MaxStreams, &expiresAt, &approvedBy, &rejectReason, &lastSeen, &c.TotalPlayMin, &reqNote, &c.EnableLog, &c.IsTester, &c.CreatedAt, &c.UpdatedAt, &c.PlanName)
	if err != nil {
		return nil, err
	}
	if expiresAt.Valid {
		c.ExpiresAt = expiresAt.Time
	}
	if lastSeen.Valid {
		c.LastSeen = lastSeen.Time
	}
	if approvedBy.Valid {
		c.ApprovedBy = approvedBy.String
	}
	if rejectReason.Valid {
		c.RejectReason = rejectReason.String
	}
	if reqNote.Valid {
		c.RequestNote = reqNote.String
	}
	// 生成预览
	if len(c.AccessToken) > 8 {
		c.TokenPreview = c.AccessToken[:8] + "..."
	}
	return &c, nil
}

func (s *ClientService) GetByToken(token string) (*models.Client, error) {
	var c models.Client
	err := s.db.QueryRow(`SELECT id, name, device_id, status, enable_log, is_tester FROM clients WHERE access_token=?`, token).
		Scan(&c.ID, &c.Name, &c.DeviceID, &c.Status, &c.EnableLog, &c.IsTester)
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
	defer func() { _ = tx.Rollback() }()

	count := 0
	now := time.Now()

	for _, id := range req.IDs {
		var err error
		switch req.Action {
		case "approve":
			token, tokenErr := generateToken()
			if tokenErr != nil {
				return 0, fmt.Errorf("生成令牌失败: %w", tokenErr)
			}
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
	_, _ = s.db.Exec(`INSERT INTO access_logs (client_id, action, channel_id, ip, user_agent, detail, created_at) VALUES (?,?,?,?,?,?,?)`,
		clientID, action, channelID, ip, userAgent, detail, time.Now())
}

func (s *ClientService) GetLogs(clientID int64, limit int, search string) ([]models.AccessLog, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "l.client_id=?"
	args := []interface{}{clientID}
	if search != "" {
		where += " AND (cl.name LIKE ? OR ch.name LIKE ? OR l.ip LIKE ? OR l.action LIKE ?)"
		sTerm := "%" + search + "%"
		args = append(args, sTerm, sTerm, sTerm, sTerm)
	}
	query := `
		SELECT l.id, l.client_id, COALESCE(cl.name, ''), l.action, l.channel_id, COALESCE(ch.name, ''), l.ip, l.user_agent, l.detail, l.created_at 
		FROM access_logs l
		LEFT JOIN clients cl ON l.client_id = cl.id
		LEFT JOIN channels ch ON l.channel_id = ch.id
		WHERE ` + where + ` 
		ORDER BY l.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var logs []models.AccessLog
	for rows.Next() {
		var l models.AccessLog
		if err := rows.Scan(&l.ID, &l.ClientID, &l.ClientName, &l.Action, &l.ChannelID, &l.ChannelName, &l.IP, &l.UserAgent, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func (s *ClientService) GetRecentLogs(limit int, search string) ([]models.AccessLog, error) {
	if limit <= 0 {
		limit = 100
	}
	where := "1=1"
	var args []interface{}
	if search != "" {
		where += " AND (cl.name LIKE ? OR ch.name LIKE ? OR l.ip LIKE ? OR l.action LIKE ?)"
		sTerm := "%" + search + "%"
		args = append(args, sTerm, sTerm, sTerm, sTerm)
	}
	query := `
		SELECT l.id, l.client_id, COALESCE(cl.name, ''), l.action, l.channel_id, COALESCE(ch.name, ''), l.ip, l.user_agent, l.detail, l.created_at 
		FROM access_logs l
		LEFT JOIN clients cl ON l.client_id = cl.id
		LEFT JOIN channels ch ON l.channel_id = ch.id
		WHERE ` + where + `
		ORDER BY l.created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var logs []models.AccessLog
	for rows.Next() {
		var l models.AccessLog
		if err := rows.Scan(&l.ID, &l.ClientID, &l.ClientName, &l.Action, &l.ChannelID, &l.ChannelName, &l.IP, &l.UserAgent, &l.Detail, &l.CreatedAt); err != nil {
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
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM clients`).Scan(&total)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE status='pending'`).Scan(&pending)
	// 5分钟内活跃
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM clients WHERE last_seen > datetime('now', '-5 minutes')`).Scan(&online)
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

// UpdateRemark 更新设备的申请备注
func (s *ClientService) UpdateRemark(id int64, note string) error {
	_, err := s.db.Exec(`UPDATE clients SET request_note=?, updated_at=DATETIME('now','localtime') WHERE id=?`, note, id)
	return err
}
