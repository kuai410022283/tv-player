package services

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

type ChannelService struct {
	db *sql.DB
}

func NewChannelService(db *sql.DB) *ChannelService {
	return &ChannelService{db: db}
}

// ── Groups ─────────────────────────────────────────────

func (s *ChannelService) ListGroups(clientID int64) ([]models.ChannelGroup, error) {
	query := `
		SELECT g.id, g.name, COALESCE(g.icon, ''), g.sort_order, g.is_direct, COALESCE(g.source, '手动'), COALESCE(g.user_agent, ''), COALESCE(g.custom_headers, ''), COALESCE(g.enable_multiplex, 0), g.created_at, g.updated_at,
		       (SELECT COUNT(*) FROM channels c WHERE c.group_id = g.id AND c.is_hidden = 0) AS channel_count,
		       (SELECT COUNT(*) FROM channels c WHERE c.group_id = g.id AND c.is_hidden = 0 AND COALESCE(c.stream_type, '') NOT IN ('ts', 'flv', 'rtmp', 'rtsp', 'octet-stream')) AS non_mux_count
		FROM channel_groups g
	`
	var args []interface{}

	if clientID > 0 {
		query += `
			JOIN plan_group_relations pgr ON g.id = pgr.group_id
			JOIN clients cl ON pgr.plan_id = cl.plan_id
			WHERE cl.id = ?
		`
		args = append(args, clientID)
	}
	
	query += ` ORDER BY CASE WHEN g.name = '未分类' THEN 1 ELSE 0 END, g.sort_order, g.id`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []models.ChannelGroup
	for rows.Next() {
		var g models.ChannelGroup
		var isDirect, nonMux int
		if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.SortOrder, &isDirect, &g.Source, &g.UserAgent, &g.CustomHeaders, &g.EnableMultiplex, &g.CreatedAt, &g.UpdatedAt, &g.ChannelCount, &nonMux); err != nil {
			return nil, err
		}
		
		// 客户端不显示空分组（没有可见频道的组）
		if g.ChannelCount == 0 {
			continue
		}

		g.IsDirect = isDirect == 1
		g.CanMultiplex = (g.ChannelCount - nonMux > 0)
		g.NonMuxCount = nonMux
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groups, nil
}

func (s *ChannelService) CreateGroup(g *models.ChannelGroup) error {
	now := time.Now()
	direct := 0
	if g.IsDirect { direct = 1 }
	if g.Source == "" { g.Source = "手动" }
	res, err := s.db.Exec(`INSERT INTO channel_groups (name, icon, sort_order, is_direct, source, user_agent, custom_headers, enable_multiplex, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.Name, g.Icon, g.SortOrder, direct, g.Source, g.UserAgent, g.CustomHeaders, g.EnableMultiplex, now, now)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	g.CreatedAt = now
	g.UpdatedAt = now
	return nil
}

func (s *ChannelService) UpdateGroup(g *models.ChannelGroup) error {
	direct := 0
	if g.IsDirect { direct = 1 }
	
	tx, err := s.db.Begin()
	if err != nil { return err }
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`UPDATE channel_groups SET name=?, icon=?, sort_order=?, is_direct=?, user_agent=?, custom_headers=?, enable_multiplex=?, updated_at=? WHERE id=?`,
		g.Name, g.Icon, g.SortOrder, direct, g.UserAgent, g.CustomHeaders, g.EnableMultiplex, time.Now(), g.ID)
	if err != nil { return err }

	// 同步修改分组下所有频道的直连和复用设置
	_, err = tx.Exec(`UPDATE channels SET is_direct=?, enable_multiplex=? WHERE group_id=?`, direct, g.EnableMultiplex, g.ID)
	if err != nil { return err }

	return tx.Commit()
}

func (s *ChannelService) DeleteGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var name, source string
	err = tx.QueryRow("SELECT name, COALESCE(source, '') FROM channel_groups WHERE id = ?", id).Scan(&name, &source)
	if err != nil {
		return err
	}
	if name == "未分类" && source == "手动" {
		return fmt.Errorf("默认分组不能删除")
	}

	// 级联删除分组下的所有频道
	_, err = tx.Exec("DELETE FROM channels WHERE group_id = ?", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM channel_groups WHERE id = ?", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *ChannelService) BatchDeleteGroups(ids []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, id := range ids {
		var name, source string
		err = tx.QueryRow("SELECT name, COALESCE(source, '') FROM channel_groups WHERE id = ?", id).Scan(&name, &source)
		if err != nil || (name == "未分类" && source == "手动") {
			continue // 忽略不存在或不能删除的默认分组
		}
		
		_, err = tx.Exec("DELETE FROM channels WHERE group_id = ?", id)
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM channel_groups WHERE id = ?", id)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *ChannelService) AdminListGroups(search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()
	where := "WHERE 1=1"
	args := []interface{}{}

	if search != "" {
		switch search {
		case "直连", "直连模式":
			where += " AND (name LIKE ? OR source LIKE ? OR is_direct = 1)"
			args = append(args, "%"+search+"%", "%"+search+"%")
		case "代理", "代理模式":
			where += " AND (name LIKE ? OR source LIKE ? OR is_direct = 0)"
			args = append(args, "%"+search+"%", "%"+search+"%")
		case "复用", "复用模式":
			where += " AND (name LIKE ? OR source LIKE ? OR enable_multiplex = 1)"
			args = append(args, "%"+search+"%", "%"+search+"%")
		default:
			where += " AND (name LIKE ? OR source LIKE ?)"
			args = append(args, "%"+search+"%", "%"+search+"%")
		}
	}

	var total int64
	if err := s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM channel_groups %s", where), args...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PageSize
	queryArgs := append(args, p.PageSize, offset)
	
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT id, name, COALESCE(icon, ''), sort_order, is_direct, COALESCE(source, '手动'), COALESCE(user_agent, ''), COALESCE(custom_headers, ''), COALESCE(enable_multiplex, 0), created_at, updated_at,
		       (SELECT COUNT(*) FROM channels c WHERE c.group_id = channel_groups.id) AS channel_count,
		       (SELECT COUNT(*) FROM channels c WHERE c.group_id = channel_groups.id AND COALESCE(c.stream_type, '') NOT IN ('ts', 'flv', 'rtmp', 'rtsp', 'octet-stream')) AS non_mux_count
		FROM channel_groups %s 
		ORDER BY CASE WHEN name = '未分类' THEN 1 ELSE 0 END, sort_order, id 
		LIMIT ? OFFSET ?`, where), queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*models.ChannelGroup
	for rows.Next() {
		m := &models.ChannelGroup{}
		var nonMux int
		if err := rows.Scan(&m.ID, &m.Name, &m.Icon, &m.SortOrder, &m.IsDirect, &m.Source, &m.UserAgent, &m.CustomHeaders, &m.EnableMultiplex, &m.CreatedAt, &m.UpdatedAt, &m.ChannelCount, &nonMux); err != nil {
			return nil, err
		}
		m.CanMultiplex = (m.ChannelCount - nonMux > 0)
		m.NonMuxCount = nonMux
		items = append(items, m)
	}

	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: items}, nil
}

// ── Channels ───────────────────────────────────────────

func (s *ChannelService) ListChannels(groupID int64, search string, muxSupport *int, p *models.PageRequest, clientID int64) (*models.PageResponse, error) {
	p.Normalize()
	var whereClauses []string
	var queryArgs []interface{}

	baseQuery := `FROM channels c LEFT JOIN channel_groups cg ON c.group_id = cg.id `

	if clientID > 0 {
		baseQuery += `
		JOIN plan_group_relations pgr ON c.group_id = pgr.group_id
		JOIN clients cl ON pgr.plan_id = cl.plan_id AND cl.id = ?
		`
		queryArgs = append(queryArgs, clientID)
	}

	if muxSupport != nil {
		if *muxSupport == 1 {
			whereClauses = append(whereClauses, "COALESCE(c.stream_type, '') IN ('ts', 'flv', 'rtmp', 'rtsp', 'octet-stream')")
		} else {
			whereClauses = append(whereClauses, "COALESCE(c.stream_type, '') NOT IN ('ts', 'flv', 'rtmp', 'rtsp', 'octet-stream')")
		}
	}

	whereClauses = append(whereClauses, "c.is_hidden = 0")

	if groupID > 0 {
		whereClauses = append(whereClauses, "c.group_id = ?")
		queryArgs = append(queryArgs, groupID)
	}
	if search != "" {
		switch search {
		case "直连", "直连模式":
			whereClauses = append(whereClauses, "(c.name LIKE ? OR cg.name LIKE ? OR c.source LIKE ? OR c.epg_channel_id LIKE ? OR c.is_direct = 1)")
			queryArgs = append(queryArgs, "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
		case "代理", "代理模式":
			whereClauses = append(whereClauses, "(c.name LIKE ? OR cg.name LIKE ? OR c.source LIKE ? OR c.epg_channel_id LIKE ? OR c.is_direct = 0)")
			queryArgs = append(queryArgs, "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
		case "复用", "复用模式":
			whereClauses = append(whereClauses, "(c.name LIKE ? OR cg.name LIKE ? OR c.source LIKE ? OR c.epg_channel_id LIKE ? OR c.enable_multiplex = 1)")
			queryArgs = append(queryArgs, "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
		default:
			whereClauses = append(whereClauses, "(c.name LIKE ? OR cg.name LIKE ? OR c.source LIKE ? OR c.epg_channel_id LIKE ?)")
			queryArgs = append(queryArgs, "%"+search+"%", "%"+search+"%", "%"+search+"%", "%"+search+"%")
		}
	}

	where := "WHERE " + strings.Join(whereClauses, " AND ")

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) "+baseQuery+where, queryArgs...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PageSize
	queryArgs = append(queryArgs, p.PageSize, offset)
	
	query := `SELECT c.id, c.group_id, c.name, COALESCE(c.logo, ''), COALESCE(c.description, ''), c.stream_url, 
		COALESCE(c.stream_type, ''), COALESCE(c.epg_channel_id, ''), 
		c.is_hidden, c.is_direct, c.sort_order, COALESCE(c.status, 'unknown'), c.last_check, COALESCE(c.source, '手动'), COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at ` +
		baseQuery + where + ` ORDER BY c.sort_order LIMIT ? OFFSET ?`
		
	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	channels := make([]models.Channel, 0)
	for rows.Next() {
		var c models.Channel
		var isHid, isDir, supportCatchup int
		var lastCheck sql.NullTime
		if err := rows.Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.Source, &c.UserAgent, &c.CustomHeaders, &supportCatchup, &c.CatchupType, &c.CatchupSource, &c.CatchupDays, &c.EnableMultiplex, &c.Fcc, &c.FccType, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.IsHidden = isHid == 1
		c.IsDirect = isDir == 1
		c.SupportCatchup = supportCatchup == 1
		if lastCheck.Valid {
			c.LastCheck = lastCheck.Time
		}

		st := strings.ToLower(c.StreamType)
		c.CanMultiplex = (st == "ts" || st == "flv" || st == "rtmp" || st == "rtsp" || st == "octet-stream")
		channels = append(channels, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: channels}, nil
}

func (s *ChannelService) GetChannel(id int64, clientID int64) (*models.Channel, error) {
	var c models.Channel
	var isHid, isDir, supportCatchup int
	var lastCheck sql.NullTime
	query := `
		SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
			c.stream_type, c.epg_channel_id, 
			c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.source, COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at 
		FROM channels c 
		WHERE c.id=?`
	args := []interface{}{id}

	if clientID > 0 {
		query = `
			SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
				c.stream_type, c.epg_channel_id, 
				c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.source, COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at 
			FROM channels c 
			JOIN plan_group_relations pgr ON c.group_id = pgr.group_id
			JOIN clients cl ON pgr.plan_id = cl.plan_id AND cl.id = ?
			WHERE c.id=?`
		args = []interface{}{clientID, id}
	}

	err := s.db.QueryRow(query, args...).
		Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.Source, &c.UserAgent, &c.CustomHeaders, &supportCatchup, &c.CatchupType, &c.CatchupSource, &c.CatchupDays, &c.EnableMultiplex, &c.Fcc, &c.FccType, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.IsHidden = isHid == 1
	c.IsDirect = isDir == 1
	c.SupportCatchup = supportCatchup == 1
	if lastCheck.Valid {
		c.LastCheck = lastCheck.Time
	}

	st := strings.ToLower(c.StreamType)
	c.CanMultiplex = (st == "ts" || st == "flv" || st == "rtmp" || st == "rtsp" || st == "octet-stream")
	return &c, nil
}

// GetInheritedHeaders computes the final user-agent and custom headers for a channel based on group inheritance.
func (s *ChannelService) GetInheritedHeaders(channelID int64) (string, map[string]string, error) {
	var groupID int64
	var chUA, chHeaders string
	err := s.db.QueryRow("SELECT group_id, COALESCE(user_agent, ''), COALESCE(custom_headers, '') FROM channels WHERE id = ?", channelID).Scan(&groupID, &chUA, &chHeaders)
	if err != nil {
		return "", nil, err
	}

	var gpUA, gpHeaders string
	if groupID > 0 {
		_ = s.db.QueryRow("SELECT COALESCE(user_agent, ''), COALESCE(custom_headers, '') FROM channel_groups WHERE id = ?", groupID).Scan(&gpUA, &gpHeaders)
	}

	// Determine final UA
	finalUA := "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player" // Global default
	if chUA != "" {
		finalUA = chUA
	} else if gpUA != "" {
		finalUA = gpUA
	}

	// Merge headers: Group headers first, then Channel headers (high priority)
	finalHeaders := make(map[string]string)
	if gpHeaders != "" {
		_ = json.Unmarshal([]byte(gpHeaders), &finalHeaders)
	}
	if chHeaders != "" {
		chMap := make(map[string]string)
		if err := json.Unmarshal([]byte(chHeaders), &chMap); err == nil {
			for k, v := range chMap {
				finalHeaders[k] = v
			}
		}
	}

	return finalUA, finalHeaders, nil
}

func (s *ChannelService) CreateChannel(c *models.Channel) error {
	// 校验流地址，防止 SSRF
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	now := time.Now()
	hid, dir, catchup := 0, 0, 0
	if c.IsHidden { hid = 1 }
	if c.IsDirect { dir = 1 }
	if c.SupportCatchup { catchup = 1 }
	if c.Source == "" { c.Source = "手动" }
	if c.StreamType == "" {
		c.StreamType = detectStreamType(c.StreamURL)
	}
	res, err := s.db.Exec(`INSERT INTO channels (group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_hidden, is_direct, sort_order, status, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days, enable_multiplex, fcc, fcc_type, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, hid, dir, c.SortOrder, "unknown", c.Source, c.UserAgent, c.CustomHeaders, catchup, c.CatchupType, c.CatchupSource, c.CatchupDays, c.EnableMultiplex, c.Fcc, c.FccType, now, now)
	if err != nil {
		return err
	}
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = now
	c.UpdatedAt = now
	return nil
}

func (s *ChannelService) UpdateChannel(c *models.Channel) error {
	// 校验流地址，防止 SSRF
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	hid, dir, catchup := 0, 0, 0
	if c.IsHidden { hid = 1 }
	if c.IsDirect { dir = 1 }
	if c.SupportCatchup { catchup = 1 }
	if c.StreamType == "" {
		c.StreamType = detectStreamType(c.StreamURL)
	}
	_, err := s.db.Exec(`UPDATE channels SET group_id=?, name=?, logo=?, description=?, stream_url=?, stream_type=?, epg_channel_id=?, is_hidden=?, is_direct=?, sort_order=?, user_agent=?, custom_headers=?, support_catchup=?, catchup_type=?, catchup_source=?, catchup_days=?, enable_multiplex=?, fcc=?, fcc_type=?, updated_at=? WHERE id=?`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, hid, dir, c.SortOrder, c.UserAgent, c.CustomHeaders, catchup, c.CatchupType, c.CatchupSource, c.CatchupDays, c.EnableMultiplex, c.Fcc, c.FccType, time.Now(), c.ID)
	return err
}

func (s *ChannelService) DeleteChannel(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func (s *ChannelService) BatchDeleteChannels(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(`DELETE FROM channels WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, id := range ids {
		if _, err := stmt.Exec(id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ChannelService) UpdateStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE channels SET status=?, last_check=? WHERE id=?`, status, time.Now(), id)
	return err
}

// UpdateStreamType updates the stream type of a channel (e.g. from auto-detection)
func (s *ChannelService) UpdateStreamType(id int64, streamType string) error {
	_, err := s.db.Exec("UPDATE channels SET stream_type = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", streamType, id)
	return err
}

func (s *ChannelService) CountByStatus(status string, count *int64) error {
	return s.db.QueryRow(`SELECT COUNT(*) FROM channels WHERE status=?`, status).Scan(count)
}

// ── Play History ───────────────────────────────────────

func (s *ChannelService) AddHistory(h *models.PlayHistory) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO play_history (channel_id, client_id, duration, last_pos, created_at) VALUES (?,?,?,?,?)`,
		h.ChannelID, h.ClientID, h.Duration, h.LastPos, now)
	if err != nil {
		return err
	}
	h.ID, _ = res.LastInsertId()
	h.CreatedAt = now

	// 更新客户端累计播放时长 (duration 秒 → 分钟)
	if h.ClientID > 0 && h.Duration > 0 {
		minutes := h.Duration / 60
		if minutes < 1 {
			minutes = 1
		}
		_, _ = s.db.Exec(`UPDATE clients SET total_play_minutes = total_play_minutes + ?, last_seen=?, updated_at=? WHERE id=?`,
			minutes, now, now, h.ClientID)
	}

	return nil
}

func (s *ChannelService) GetHistory(limit int) ([]models.PlayHistory, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id, channel_id, client_id, duration, last_pos, created_at FROM play_history ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.PlayHistory
	for rows.Next() {
		var h models.PlayHistory
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.ClientID, &h.Duration, &h.LastPos, &h.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// ── Settings ───────────────────────────────────────────

func (s *ChannelService) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key=?`, key).Scan(&val)
	return val, err
}

func (s *ChannelService) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO user_settings (key, value) VALUES (?,?)`, key, value)
	return err
}

func (s *ChannelService) GetAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM user_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// ── M3U Sources ────────────────────────────────────────

func (s *ChannelService) ListM3USources() ([]models.M3USource, error) {
	rows, err := s.db.Query(`SELECT id, name, url, auto_sync, sync_interval, COALESCE(user_agent, ''), COALESCE(custom_headers, ''), last_sync, COALESCE(sync_status, 'idle'), COALESCE(sync_error, ''), created_at FROM m3u_sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.M3USource
	for rows.Next() {
		var m models.M3USource
		var autoSync int
		var syncInterval int
		var lastSync sql.NullTime
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &autoSync, &syncInterval, &m.UserAgent, &m.CustomHeaders, &lastSync, &m.SyncStatus, &m.SyncError, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.AutoSync = autoSync == 1
		m.SyncInterval = syncInterval
		if lastSync.Valid {
			m.LastSync = lastSync.Time
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *ChannelService) AddM3USource(m *models.M3USource) error {
	now := time.Now()
	autoSyncInt := 0
	if m.AutoSync { autoSyncInt = 1 }
	if m.SyncInterval <= 0 { m.SyncInterval = 12 }
	res, err := s.db.Exec(`INSERT INTO m3u_sources (name, url, auto_sync, sync_interval, user_agent, custom_headers, created_at) VALUES (?,?,?,?,?,?,?)`, m.Name, m.URL, autoSyncInt, m.SyncInterval, m.UserAgent, m.CustomHeaders, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	return nil
}

func (s *ChannelService) UpdateM3USource(m *models.M3USource) error {
	autoSyncInt := 0
	if m.AutoSync { autoSyncInt = 1 }
	if m.SyncInterval <= 0 { m.SyncInterval = 12 }
	_, err := s.db.Exec(`UPDATE m3u_sources SET name=?, url=?, auto_sync=?, sync_interval=?, user_agent=?, custom_headers=? WHERE id=?`, m.Name, m.URL, autoSyncInt, m.SyncInterval, m.UserAgent, m.CustomHeaders, m.ID)
	return err
}

func (s *ChannelService) DeleteM3USource(id int64) error {
	_, err := s.db.Exec(`DELETE FROM m3u_sources WHERE id=?`, id)
	return err
}

// ── EPG ────────────────────────────────────────────────

func (s *ChannelService) GetEPGPrograms(channelID string) ([]models.EPGProgram, error) {
	rows, err := s.db.Query(`SELECT id, epg_channel_id, title, start_time, end_time, description FROM epg_programs WHERE epg_channel_id=? ORDER BY start_time`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.EPGProgram
	for rows.Next() {
		var p models.EPGProgram
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.Title, &p.StartTime, &p.EndTime, &p.Desc); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
