package services

import (
	"database/sql"
	"fmt"
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
	rows, err := s.db.Query(`SELECT id, name, icon, sort_order, created_at, updated_at FROM channel_groups ORDER BY sort_order`)
	if err != nil { return nil, err }
	defer rows.Close()

	var groups []models.ChannelGroup
	for rows.Next() {
		var g models.ChannelGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Icon, &g.SortOrder, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func (s *ChannelService) CreateGroup(g *models.ChannelGroup) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO channel_groups (name, icon, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		g.Name, g.Icon, g.SortOrder, now, now)
	if err != nil { return err }
	g.ID, _ = res.LastInsertId()
	g.CreatedAt = now
	g.UpdatedAt = now
	return nil
}

func (s *ChannelService) UpdateGroup(g *models.ChannelGroup) error {
	_, err := s.db.Exec(`UPDATE channel_groups SET name=?, icon=?, sort_order=?, updated_at=? WHERE id=?`,
		g.Name, g.Icon, g.SortOrder, time.Now(), g.ID)
	return err
}

func (s *ChannelService) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channel_groups WHERE id=?`, id)
	return err
}

// ── Channels ───────────────────────────────────────────

func (s *ChannelService) ListChannels(groupID int64, favorite bool, search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()
	where := "WHERE is_hidden = 0"
	args := []interface{}{}

	if groupID > 0 {
		where += " AND group_id = ?"
		args = append(args, groupID)
	}
	if favorite {
		where += " AND is_favorite = 1"
	}
	if search != "" {
		where += " AND name LIKE ?"
		args = append(args, "%"+search+"%")
	}

	var total int64
	countArgs := append([]interface{}{}, args...)
	s.db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM channels %s", where), countArgs...).Scan(&total)

	offset := (p.Page - 1) * p.PageSize
	queryArgs := append(args, p.PageSize, offset)
	rows, err := s.db.Query(fmt.Sprintf(
		"SELECT id, group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, sort_order, status, last_check, created_at, updated_at FROM channels %s ORDER BY sort_order LIMIT ? OFFSET ?", where),
		queryArgs...,
	)
	if err != nil { return nil, err }
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var c models.Channel
		if err := rows.Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &c.IsFavorite, &c.IsHidden, &c.SortOrder, &c.Status, &c.LastCheck, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		channels = append(channels, c)
	}
	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: channels}, nil
}

func (s *ChannelService) GetChannel(id int64) (*models.Channel, error) {
	var c models.Channel
	err := s.db.QueryRow(`SELECT id, group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, sort_order, status, last_check, created_at, updated_at FROM channels WHERE id=?`, id).
		Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &c.IsFavorite, &c.IsHidden, &c.SortOrder, &c.Status, &c.LastCheck, &c.CreatedAt, &c.UpdatedAt)
	if err != nil { return nil, err }
	return &c, nil
}

func (s *ChannelService) CreateChannel(c *models.Channel) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO channels (group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, sort_order, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, c.IsFavorite, c.IsHidden, c.SortOrder, "unknown", now, now)
	if err != nil { return err }
	c.ID, _ = res.LastInsertId()
	c.CreatedAt = now
	c.UpdatedAt = now
	return nil
}

func (s *ChannelService) UpdateChannel(c *models.Channel) error {
	_, err := s.db.Exec(`UPDATE channels SET group_id=?, name=?, logo=?, description=?, stream_url=?, stream_type=?, epg_channel_id=?, is_favorite=?, is_hidden=?, sort_order=?, updated_at=? WHERE id=?`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, c.IsFavorite, c.IsHidden, c.SortOrder, time.Now(), c.ID)
	return err
}

func (s *ChannelService) DeleteChannel(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func (s *ChannelService) ToggleFavorite(id int64) error {
	_, err := s.db.Exec(`UPDATE channels SET is_favorite = CASE WHEN is_favorite=1 THEN 0 ELSE 1 END, updated_at=? WHERE id=?`, time.Now(), id)
	return err
}

func (s *ChannelService) UpdateStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE channels SET status=?, last_check=? WHERE id=?`, status, time.Now(), id)
	return err
}

// ── Play History ───────────────────────────────────────

func (s *ChannelService) AddHistory(h *models.PlayHistory) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO play_history (channel_id, duration, last_pos, created_at) VALUES (?,?,?,?)`,
		h.ChannelID, h.Duration, h.LastPos, now)
	if err != nil { return err }
	h.ID, _ = res.LastInsertId()
	h.CreatedAt = now
	return nil
}

func (s *ChannelService) GetHistory(limit int) ([]models.PlayHistory, error) {
	if limit <= 0 { limit = 50 }
	rows, err := s.db.Query(`SELECT id, channel_id, duration, last_pos, created_at FROM play_history ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil { return nil, err }
	defer rows.Close()

	var items []models.PlayHistory
	for rows.Next() {
		var h models.PlayHistory
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.Duration, &h.LastPos, &h.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, h)
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
	if err != nil { return nil, err }
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		m[k] = v
	}
	return m, nil
}

// ── M3U Sources ────────────────────────────────────────

func (s *ChannelService) ListM3USources() ([]models.M3USource, error) {
	rows, err := s.db.Query(`SELECT id, name, url, auto_sync, last_sync, created_at FROM m3u_sources ORDER BY created_at DESC`)
	if err != nil { return nil, err }
	defer rows.Close()

	var items []models.M3USource
	for rows.Next() {
		var m models.M3USource
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.AutoSync, &m.LastSync, &m.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, nil
}

func (s *ChannelService) AddM3USource(m *models.M3USource) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO m3u_sources (name, url, auto_sync, created_at) VALUES (?,?,?,?)`, m.Name, m.URL, m.AutoSync, now)
	if err != nil { return err }
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	return nil
}

func (s *ChannelService) DeleteM3USource(id int64) error {
	_, err := s.db.Exec(`DELETE FROM m3u_sources WHERE id=?`, id)
	return err
}
