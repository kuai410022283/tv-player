package services

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

type ChannelService struct {
	db *sql.DB
}

func NewChannelService(db *sql.DB) *ChannelService {
	return &ChannelService{db: db}
}

// ── Groups ─────────────────────────────────────────────

func (s *ChannelService) ListGroups() ([]models.ChannelGroup, error) {
	rows, err := s.db.Query(`SELECT id, name, icon, sort_order, is_direct, created_at, updated_at FROM channel_groups ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []models.ChannelGroup
	for rows.Next() {
		var g models.ChannelGroup
		var isDirect int
		if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.SortOrder, &isDirect, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.IsDirect = isDirect == 1
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
	res, err := s.db.Exec(`INSERT INTO channel_groups (name, icon, sort_order, is_direct, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		g.Name, g.Icon, g.SortOrder, direct, now, now)
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

	_, err = tx.Exec(`UPDATE channel_groups SET name=?, icon=?, sort_order=?, is_direct=?, updated_at=? WHERE id=?`,
		g.Name, g.Icon, g.SortOrder, direct, time.Now(), g.ID)
	if err != nil { return err }

	// 同步修改分组下所有频道的直连设置
	_, err = tx.Exec(`UPDATE channels SET is_direct=? WHERE group_id=?`, direct, g.ID)
	if err != nil { return err }

	return tx.Commit()
}

func (s *ChannelService) DeleteGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var defaultGroupID int64
	err = tx.QueryRow("SELECT id FROM channel_groups WHERE name = '未分类'").Scan(&defaultGroupID)
	if err != nil {
		res, err := tx.Exec("INSERT INTO channel_groups (name, sort_order) VALUES ('未分类', 99)")
		if err != nil {
			return err
		}
		defaultGroupID, _ = res.LastInsertId()
	}

	_, err = tx.Exec("UPDATE channels SET group_id = ?, updated_at = ? WHERE group_id = ?", defaultGroupID, time.Now(), id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("DELETE FROM channel_groups WHERE id = ?", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// ── Channels ───────────────────────────────────────────

func (s *ChannelService) ListChannels(groupID int64, favorite bool, search string, p *models.PageRequest, clientID int64) (*models.PageResponse, error) {
	p.Normalize()
	var whereClauses []string
	var queryArgs []interface{}

	queryArgs = append(queryArgs, clientID)
	baseQuery := `FROM channels c LEFT JOIN client_channel_favorites f ON c.id = f.channel_id AND f.client_id = ? `

	whereClauses = append(whereClauses, "c.is_hidden = 0")

	if groupID > 0 {
		whereClauses = append(whereClauses, "c.group_id = ?")
		queryArgs = append(queryArgs, groupID)
	}
	if favorite {
		whereClauses = append(whereClauses, "f.channel_id IS NOT NULL")
	}
	if search != "" {
		whereClauses = append(whereClauses, "c.name LIKE ?")
		queryArgs = append(queryArgs, "%"+search+"%")
	}

	where := "WHERE " + strings.Join(whereClauses, " AND ")

	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) "+baseQuery+where, queryArgs...).Scan(&total); err != nil {
		return nil, err
	}

	offset := (p.Page - 1) * p.PageSize
	queryArgs = append(queryArgs, p.PageSize, offset)
	
	query := `SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
		c.stream_type, c.epg_channel_id, 
		CASE WHEN f.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite, 
		c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.created_at, c.updated_at ` +
		baseQuery + where + ` ORDER BY c.sort_order LIMIT ? OFFSET ?`
		
	rows, err := s.db.Query(query, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var c models.Channel
		var isFav, isHid, isDir int
		var lastCheck sql.NullTime
		if err := rows.Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isFav, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		c.IsFavorite = isFav == 1
		c.IsHidden = isHid == 1
		c.IsDirect = isDir == 1
		if lastCheck.Valid {
			c.LastCheck = lastCheck.Time
		}
		channels = append(channels, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: channels}, nil
}

func (s *ChannelService) GetChannel(id int64, clientID int64) (*models.Channel, error) {
	var c models.Channel
	var isFav, isHid, isDir int
	var lastCheck sql.NullTime
	err := s.db.QueryRow(`
		SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
			c.stream_type, c.epg_channel_id, 
			CASE WHEN f.channel_id IS NOT NULL THEN 1 ELSE 0 END as is_favorite, 
			c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.created_at, c.updated_at 
		FROM channels c 
		LEFT JOIN client_channel_favorites f ON c.id = f.channel_id AND f.client_id = ?
		WHERE c.id=?`, clientID, id).
		Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isFav, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.IsFavorite = isFav == 1
	c.IsHidden = isHid == 1
	c.IsDirect = isDir == 1
	if lastCheck.Valid {
		c.LastCheck = lastCheck.Time
	}
	return &c, nil
}

func (s *ChannelService) CreateChannel(c *models.Channel) error {
	// 校验流地址，防止 SSRF
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	now := time.Now()
	fav, hid, dir := 0, 0, 0
	if c.IsFavorite { fav = 1 }
	if c.IsHidden { hid = 1 }
	if c.IsDirect { dir = 1 }
	res, err := s.db.Exec(`INSERT INTO channels (group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, is_direct, sort_order, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, fav, hid, dir, c.SortOrder, "unknown", now, now)
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

	fav, hid, dir := 0, 0, 0
	if c.IsFavorite { fav = 1 }
	if c.IsHidden { hid = 1 }
	if c.IsDirect { dir = 1 }
	_, err := s.db.Exec(`UPDATE channels SET group_id=?, name=?, logo=?, description=?, stream_url=?, stream_type=?, epg_channel_id=?, is_favorite=?, is_hidden=?, is_direct=?, sort_order=?, updated_at=? WHERE id=?`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, fav, hid, dir, c.SortOrder, time.Now(), c.ID)
	return err
}

func (s *ChannelService) DeleteChannel(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func (s *ChannelService) ToggleFavorite(id int64, clientID int64) error {
	res, err := s.db.Exec(`DELETE FROM client_channel_favorites WHERE client_id=? AND channel_id=?`, clientID, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		_, err = s.db.Exec(`INSERT INTO client_channel_favorites (client_id, channel_id) VALUES (?,?)`, clientID, id)
		return err
	}
	return nil
}

func (s *ChannelService) UpdateStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE channels SET status=?, last_check=? WHERE id=?`, status, time.Now(), id)
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
	rows, err := s.db.Query(`SELECT id, name, url, auto_sync, last_sync, created_at FROM m3u_sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []models.M3USource
	for rows.Next() {
		var m models.M3USource
		var autoSync int
		var lastSync sql.NullTime
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &autoSync, &lastSync, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.AutoSync = autoSync == 1
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
	res, err := s.db.Exec(`INSERT INTO m3u_sources (name, url, auto_sync, created_at) VALUES (?,?,?,?)`, m.Name, m.URL, autoSyncInt, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	return nil
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
