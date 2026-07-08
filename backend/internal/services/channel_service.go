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

func (s *ChannelService) ListGroups(clientID int64, includeEmpty bool) ([]models.ChannelGroup, error) {
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
		// 客户端请求时使用套餐级别的分组排序
		query += ` ORDER BY CASE WHEN g.name = '未分类' THEN 1 ELSE 0 END, pgr.sort_order, g.id`
	} else {
		// 管理端请求时使用全局分组排序
		query += ` ORDER BY CASE WHEN g.name = '未分类' THEN 1 ELSE 0 END, g.sort_order, g.id`
	}

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
		if !includeEmpty && g.ChannelCount == 0 {
			continue
		}

		g.IsDirect = isDirect == 1
		g.CanMultiplex = (g.ChannelCount-nonMux > 0)
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
	if g.IsDirect {
		direct = 1
	}
	if g.Source == "" {
		g.Source = "手动"
	}
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
	if g.IsDirect {
		direct = 1
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`UPDATE channel_groups SET name=?, icon=?, sort_order=?, is_direct=?, user_agent=?, custom_headers=?, enable_multiplex=?, updated_at=? WHERE id=?`,
		g.Name, g.Icon, g.SortOrder, direct, g.UserAgent, g.CustomHeaders, g.EnableMultiplex, time.Now(), g.ID)
	if err != nil {
		return err
	}

	// 同步修改分组下所有频道的直连和复用设置
	_, err = tx.Exec(`UPDATE channels SET is_direct=?, enable_multiplex=? WHERE group_id=?`, direct, g.EnableMultiplex, g.ID)
	if err != nil {
		return err
	}

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

	err = tx.Commit()
	if err == nil {
		// 删除分组后，重建所有分组的排序，消除空洞
		_ = s.ReorderAllGroups()
	}
	return err
}

func (s *ChannelService) BatchUpdateGroups(ids []int64, action string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var groupQuery string
	var channelQuery string
	switch action {
	case "direct_on":
		groupQuery = "UPDATE channel_groups SET is_direct = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
		channelQuery = "UPDATE channels SET is_direct = 1, updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "direct_off":
		groupQuery = "UPDATE channel_groups SET is_direct = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
		channelQuery = "UPDATE channels SET is_direct = 0, updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "mux_on":
		groupQuery = "UPDATE channel_groups SET enable_multiplex = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
		channelQuery = "UPDATE channels SET enable_multiplex = 1, updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "mux_off":
		groupQuery = "UPDATE channel_groups SET enable_multiplex = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
		channelQuery = "UPDATE channels SET enable_multiplex = 0, updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "content_type_auto":
		channelQuery = "UPDATE channels SET content_type = '', updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "content_type_live":
		channelQuery = "UPDATE channels SET content_type = 'live', updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	case "content_type_vod":
		channelQuery = "UPDATE channels SET content_type = 'vod', updated_at = CURRENT_TIMESTAMP WHERE group_id = ?"
	default:
		return fmt.Errorf("invalid action")
	}

	var stmtGroup *sql.Stmt
	if groupQuery != "" {
		stmtGroup, err = tx.Prepare(groupQuery)
		if err != nil {
			return err
		}
		defer stmtGroup.Close()
	}

	var stmtChannel *sql.Stmt
	if channelQuery != "" {
		stmtChannel, err = tx.Prepare(channelQuery)
		if err != nil {
			return err
		}
		defer stmtChannel.Close()
	}

	for _, id := range ids {
		if stmtGroup != nil {
			if _, err := stmtGroup.Exec(id); err != nil {
				return err
			}
		}
		if stmtChannel != nil {
			if _, err := stmtChannel.Exec(id); err != nil {
				return err
			}
		}
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

	err = tx.Commit()
	if err == nil {
		// 删除分组后，重建所有分组的排序，消除空洞
		_ = s.ReorderAllGroups()
	}
	return err
}

// BatchUpdateGroupSort 批量更新分组排序
func (s *ChannelService) BatchUpdateGroupSort(items []struct {
	ID    int64 `json:"id"`
	Order int   `json:"sort_order"`
}) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare("UPDATE channel_groups SET sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, item := range items {
		if _, err := stmt.Exec(item.Order, item.ID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ReorderAllGroups 重新排序所有分组（删除后调用，消除 sort_order 空洞）
func (s *ChannelService) ReorderAllGroups() error {
	rows, err := s.db.Query(`SELECT id FROM channel_groups ORDER BY CASE WHEN name = '未分类' THEN 1 ELSE 0 END, sort_order, id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare("UPDATE channel_groups SET sort_order = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, id := range ids {
		if _, err := stmt.Exec(i, id); err != nil {
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
		m.CanMultiplex = (m.ChannelCount-nonMux > 0)
		m.NonMuxCount = nonMux
		items = append(items, m)
	}

	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: items}, nil
}

// ── Channels ───────────────────────────────────────────

func (s *ChannelService) ListChannels(groupID int64, search string, source string, muxSupport *int, p *models.PageRequest, clientID int64) (*models.PageResponse, error) {
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
	if source != "" {
		whereClauses = append(whereClauses, "c.source = ?")
		queryArgs = append(queryArgs, source)
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
		c.is_hidden, c.is_direct, c.sort_order, COALESCE(c.status, 'unknown'), c.last_check, COALESCE(c.source, '手动'), COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.content_type, ''), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at ` +
		baseQuery + where
	if clientID > 0 {
		// 客户端请求时使用套餐级别的分组排序，严格遵循套餐管理中设定的分组先后顺序，剔除 c.source 的干扰
		query += ` ORDER BY pgr.sort_order, c.sort_order, c.id LIMIT ? OFFSET ?`
	} else {
		// 管理端请求时使用全局分组排序 (加入未分类垫底和分组自定义排序)
		query += ` ORDER BY c.source, CASE WHEN cg.name = '未分类' THEN 1 ELSE 0 END, cg.sort_order, cg.id, c.sort_order, c.id LIMIT ? OFFSET ?`
	}

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
		if err := rows.Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.Source, &c.UserAgent, &c.CustomHeaders, &supportCatchup, &c.CatchupType, &c.CatchupSource, &c.CatchupDays, &c.EnableMultiplex, &c.ContentType, &c.Fcc, &c.FccType, &c.CreatedAt, &c.UpdatedAt); err != nil {
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

// GetDistinctSources 获取所有不重复的频道来源列表
func (s *ChannelService) GetDistinctSources() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT source FROM channels WHERE is_hidden = 0 ORDER BY source`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var sources []string
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return sources, rows.Err()
}

// BatchUpdateChannelSort 批量更新频道排序（按来源+分组隔离）
func (s *ChannelService) BatchUpdateChannelSort(items []struct {
	ID    int64 `json:"id"`
	Order int   `json:"sort_order"`
}, groupID int64, source string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare("UPDATE channels SET sort_order = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, item := range items {
		if _, err := stmt.Exec(item.Order, item.ID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ReorderChannels 按 group_id + source 独立重新排序频道（消除 sort_order 空洞）
func (s *ChannelService) ReorderChannels(groupID int64, source string) error {
	where := "WHERE c.is_hidden = 0"
	args := []interface{}{}
	if groupID > 0 {
		where += " AND c.group_id = ?"
		args = append(args, groupID)
	}
	if source != "" {
		where += " AND c.source = ?"
		args = append(args, source)
	}

	// 联表 channel_groups 以确保排序逻辑和列表一致（尽管对于重排本身，只要分组聚拢即可）
	query := `SELECT c.id, c.group_id, c.source FROM channels c LEFT JOIN channel_groups cg ON c.group_id = cg.id ` + where + ` ORDER BY c.source, CASE WHEN cg.name = '未分类' THEN 1 ELSE 0 END, cg.sort_order, cg.id, c.sort_order, c.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	type chInfo struct {
		id      int64
		groupID int64
		source  string
	}
	var items []chInfo
	for rows.Next() {
		var item chInfo
		if err := rows.Scan(&item.id, &item.groupID, &item.source); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare("UPDATE channels SET sort_order = ? WHERE id = ?")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	// 为每个 (group_id, source) 维护独立的排序计数器
	counters := make(map[string]int)
	for _, item := range items {
		key := fmt.Sprintf("%d|%s", item.groupID, item.source)
		seq := counters[key]
		counters[key]++

		if _, err := stmt.Exec(seq, item.id); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetNextChannelSortOrder 获取指定 group_id + source 下的最大 sort_order + 1
func (s *ChannelService) GetNextChannelSortOrder(groupID int64, source string) int {
	var maxOrder int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) FROM channels WHERE group_id = ? AND source = ?`, groupID, source).Scan(&maxOrder)
	if err != nil {
		return 0
	}
	return maxOrder + 1
}

func (s *ChannelService) GetChannel(id int64, clientID int64) (*models.Channel, error) {
	var c models.Channel
	var isHid, isDir, supportCatchup int
	var lastCheck sql.NullTime
	query := `
		SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
			c.stream_type, c.epg_channel_id, 
			c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.source, COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.content_type, ''), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at 
		FROM channels c 
		WHERE c.id=?`
	args := []interface{}{id}

	if clientID > 0 {
		query = `
			SELECT c.id, c.group_id, c.name, c.logo, c.description, c.stream_url, 
				c.stream_type, c.epg_channel_id, 
				c.is_hidden, c.is_direct, c.sort_order, c.status, c.last_check, c.source, COALESCE(c.user_agent, ''), COALESCE(c.custom_headers, ''), c.support_catchup, COALESCE(c.catchup_type, ''), COALESCE(c.catchup_source, ''), c.catchup_days, COALESCE(c.enable_multiplex, 0), COALESCE(c.content_type, ''), COALESCE(c.fcc, ''), COALESCE(c.fcc_type, ''), c.created_at, c.updated_at 
			FROM channels c 
			JOIN plan_group_relations pgr ON c.group_id = pgr.group_id
			JOIN clients cl ON pgr.plan_id = cl.plan_id AND cl.id = ?
			WHERE c.id=?`
		args = []interface{}{clientID, id}
	}

	err := s.db.QueryRow(query, args...).
		Scan(&c.ID, &c.GroupID, &c.Name, &c.Logo, &c.Description, &c.StreamURL, &c.StreamType, &c.EPGChannelID, &isHid, &isDir, &c.SortOrder, &c.Status, &lastCheck, &c.Source, &c.UserAgent, &c.CustomHeaders, &supportCatchup, &c.CatchupType, &c.CatchupSource, &c.CatchupDays, &c.EnableMultiplex, &c.ContentType, &c.Fcc, &c.FccType, &c.CreatedAt, &c.UpdatedAt)
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

	return s.computeInheritedHeaders(chUA, chHeaders, gpUA, gpHeaders)
}

// computeInheritedHeaders 合并频道和分组的 UA/Headers
func (s *ChannelService) computeInheritedHeaders(chUA, chHeaders, gpUA, gpHeaders string) (string, map[string]string, error) {
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

// BatchGetInheritedHeaders 批量获取多个频道的继承头信息，避免 N+1 查询
// 返回 map[channelID]struct{UA, Headers}
type InheritedHeaderInfo struct {
	UA      string
	Headers map[string]string
}

const sqliteMaxBatchSize = 500 // SQLite IN 子句安全批次大小

func (s *ChannelService) BatchGetInheritedHeaders(channelIDs []int64) (map[int64]*InheritedHeaderInfo, error) {
	if len(channelIDs) == 0 {
		return nil, nil
	}

	type channelInfo struct {
		groupID   int64
		chUA      string
		chHeaders string
	}
	channelMap := make(map[int64]*channelInfo, len(channelIDs))
	groupIDs := make(map[int64]bool)

	// 分批查询频道，避免 SQLite IN 子句参数限制
	for i := 0; i < len(channelIDs); i += sqliteMaxBatchSize {
		end := i + sqliteMaxBatchSize
		if end > len(channelIDs) {
			end = len(channelIDs)
		}
		batch := channelIDs[i:end]

		placeholders := make([]string, len(batch))
		args := make([]interface{}, len(batch))
		for j, id := range batch {
			placeholders[j] = "?"
			args[j] = id
		}

		query := fmt.Sprintf(
			"SELECT id, group_id, COALESCE(user_agent, ''), COALESCE(custom_headers, '') FROM channels WHERE id IN (%s)",
			strings.Join(placeholders, ","),
		)

		rows, err := s.db.Query(query, args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var id, groupID int64
			var chUA, chHeaders string
			if err := rows.Scan(&id, &groupID, &chUA, &chHeaders); err != nil {
				continue
			}
			channelMap[id] = &channelInfo{groupID: groupID, chUA: chUA, chHeaders: chHeaders}
			if groupID > 0 {
				groupIDs[groupID] = true
			}
		}
		rows.Close()
	}

	// 批量查询分组的 UA/Headers（分组数量通常很少，不需要分批）
	groupHeaderMap := make(map[int64]struct{ ua, headers string })
	if len(groupIDs) > 0 {
		gidList := make([]int64, 0, len(groupIDs))
		for gid := range groupIDs {
			gidList = append(gidList, gid)
		}

		// 分组数量通常远小于频道数量，但为安全起见也分批
		for i := 0; i < len(gidList); i += sqliteMaxBatchSize {
			end := i + sqliteMaxBatchSize
			if end > len(gidList) {
				end = len(gidList)
			}
			batch := gidList[i:end]

			placeholders := make([]string, len(batch))
			args := make([]interface{}, len(batch))
			for j, gid := range batch {
				placeholders[j] = "?"
				args[j] = gid
			}

			query := fmt.Sprintf(
				"SELECT id, COALESCE(user_agent, ''), COALESCE(custom_headers, '') FROM channel_groups WHERE id IN (%s)",
				strings.Join(placeholders, ","),
			)

			rows, err := s.db.Query(query, args...)
			if err == nil {
				for rows.Next() {
					var gid int64
					var ua, headers string
					if err := rows.Scan(&gid, &ua, &headers); err == nil {
						groupHeaderMap[gid] = struct{ ua, headers string }{ua: ua, headers: headers}
					}
				}
				rows.Close()
			}
		}
	}

	// 合并结果
	result := make(map[int64]*InheritedHeaderInfo, len(channelIDs))
	for id, chInfo := range channelMap {
		gpUA, gpHeaders := "", ""
		if gh, ok := groupHeaderMap[chInfo.groupID]; ok {
			gpUA = gh.ua
			gpHeaders = gh.headers
		}

		ua, headers, _ := s.computeInheritedHeaders(chInfo.chUA, chInfo.chHeaders, gpUA, gpHeaders)
		result[id] = &InheritedHeaderInfo{UA: ua, Headers: headers}
	}

	return result, nil
}

func (s *ChannelService) CreateChannel(c *models.Channel) error {
	// 校验流地址，防止 SSRF
	if err := ValidateStreamURL(c.StreamURL); err != nil {
		return fmt.Errorf("流地址不安全: %w", err)
	}

	now := time.Now()
	hid, dir, catchup := 0, 0, 0
	if c.IsHidden {
		hid = 1
	}
	if c.IsDirect {
		dir = 1
	}
	if c.SupportCatchup {
		catchup = 1
	}
	if c.Source == "" {
		c.Source = "手动"
	}
	if c.StreamType == "" {
		c.StreamType = detectStreamType(c.StreamURL)
	}
	res, err := s.db.Exec(`INSERT INTO channels (group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_hidden, is_direct, sort_order, status, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days, enable_multiplex, content_type, fcc, fcc_type, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, hid, dir, c.SortOrder, "unknown", c.Source, c.UserAgent, c.CustomHeaders, catchup, c.CatchupType, c.CatchupSource, c.CatchupDays, c.EnableMultiplex, c.ContentType, c.Fcc, c.FccType, now, now)
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
	if c.IsHidden {
		hid = 1
	}
	if c.IsDirect {
		dir = 1
	}
	if c.SupportCatchup {
		catchup = 1
	}
	if c.StreamType == "" {
		c.StreamType = detectStreamType(c.StreamURL)
	}
	_, err := s.db.Exec(`UPDATE channels SET group_id=?, name=?, logo=?, description=?, stream_url=?, stream_type=?, epg_channel_id=?, is_hidden=?, is_direct=?, sort_order=?, user_agent=?, custom_headers=?, support_catchup=?, catchup_type=?, catchup_source=?, catchup_days=?, enable_multiplex=?, content_type=?, fcc=?, fcc_type=?, updated_at=? WHERE id=?`,
		c.GroupID, c.Name, c.Logo, c.Description, c.StreamURL, c.StreamType, c.EPGChannelID, hid, dir, c.SortOrder, c.UserAgent, c.CustomHeaders, catchup, c.CatchupType, c.CatchupSource, c.CatchupDays, c.EnableMultiplex, c.ContentType, c.Fcc, c.FccType, time.Now(), c.ID)
	return err
}

func (s *ChannelService) DeleteChannel(id int64) error {
	_, err := s.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	if err == nil {
		// 删除频道后，可能产生排序空洞，重新梳理
		_ = s.ReorderChannels(-1, "")
	}
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
	err = tx.Commit()
	if err == nil {
		// 批量删除频道后，可能产生大量排序空洞，重新梳理
		_ = s.ReorderChannels(-1, "")
	}
	return err
}

func (s *ChannelService) BatchUpdateChannels(ids []int64, action string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var query string
	switch action {
	case "direct_on":
		query = "UPDATE channels SET is_direct = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "direct_off":
		query = "UPDATE channels SET is_direct = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "mux_on":
		query = "UPDATE channels SET enable_multiplex = 1, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "mux_off":
		query = "UPDATE channels SET enable_multiplex = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "content_type_auto":
		query = "UPDATE channels SET content_type = '', updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "content_type_live":
		query = "UPDATE channels SET content_type = 'live', updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	case "content_type_vod":
		query = "UPDATE channels SET content_type = 'vod', updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	default:
		return fmt.Errorf("invalid action")
	}

	stmt, err := tx.Prepare(query)
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

// UpdateStreamType updates the stream type of a specific line for a channel (e.g. from auto-detection)
func (s *ChannelService) UpdateStreamType(id int64, lineIdx int, streamType string) error {
	var currentType string
	err := s.db.QueryRow("SELECT COALESCE(stream_type, '') FROM channels WHERE id = ?", id).Scan(&currentType)
	if err != nil {
		return err
	}

	types := strings.Split(currentType, "#")
	// Pad the types slice if it's smaller than lineIdx
	for len(types) <= lineIdx {
		types = append(types, "")
	}
	types[lineIdx] = streamType
	newStreamType := strings.Join(types, "#")

	_, err = s.db.Exec("UPDATE channels SET stream_type = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", newStreamType, id)
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
	if err != nil {
		return err
	}

	// 特殊处理：更新本地文件开关
	if key == "allow_local_file" {
		AllowLocalFile = value == "true" || value == "1"
	}

	return nil
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
	if m.AutoSync {
		autoSyncInt = 1
	}
	if m.SyncInterval <= 0 {
		m.SyncInterval = 12
	}
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
	if m.AutoSync {
		autoSyncInt = 1
	}
	if m.SyncInterval <= 0 {
		m.SyncInterval = 12
	}
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
