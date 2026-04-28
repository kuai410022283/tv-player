// ========================================
// channel_service.go — ListChannels 安全改写
// 使用参数化查询替代 fmt.Sprintf 拼接
// ========================================

package services

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

// ListChannels 安全改写，避免 fmt.Sprintf 拼接 SQL
func (s *ChannelService) ListChannels(groupID int64, favorite bool, search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()

	// ★ 改用 strings.Builder + 参数切片构建查询
	var whereClauses []string
	var args []interface{}

	whereClauses = append(whereClauses, "is_hidden = 0")

	if groupID > 0 {
		whereClauses = append(whereClauses, "group_id = ?")
		args = append(args, groupID)
	}
	if favorite {
		whereClauses = append(whereClauses, "is_favorite = 1")
	}
	if search != "" {
		whereClauses = append(whereClauses, "name LIKE ?")
		args = append(args, "%"+search+"%")
	}

	where := strings.Join(whereClauses, " AND ")

	// COUNT 查询
	var total int64
	countQuery := "SELECT COUNT(*) FROM channels WHERE " + where
	s.db.QueryRow(countQuery, args...).Scan(&total)

	// 数据查询
	offset := (p.Page - 1) * p.PageSize
	dataQuery := "SELECT id, group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_favorite, is_hidden, sort_order, status, last_check, created_at, updated_at FROM channels WHERE " + where + " ORDER BY sort_order LIMIT ? OFFSET ?"
	queryArgs := append(args, p.PageSize, offset)

	rows, err := s.db.Query(dataQuery, queryArgs...)
	if err != nil {
		return nil, err
	}
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

// List 安全改写（client_service.go 同理）
func (s *ClientService) List(status string, search string, p *models.PageRequest) (*models.PageResponse, error) {
	p.Normalize()

	var whereClauses []string
	var args []interface{}

	whereClauses = append(whereClauses, "1=1")

	if status != "" {
		whereClauses = append(whereClauses, "status=?")
		args = append(args, status)
	}
	if search != "" {
		whereClauses = append(whereClauses, "(name LIKE ? OR device_id LIKE ? OR device_model LIKE ? OR ip LIKE ?)")
		s := "%" + search + "%"
		args = append(args, s, s, s, s)
	}

	where := strings.Join(whereClauses, " AND ")

	var total int64
	countQuery := "SELECT COUNT(*) FROM clients WHERE " + where
	s.db.QueryRow(countQuery, args...).Scan(&total)

	offset := (p.Page - 1) * p.PageSize
	dataQuery := fmt.Sprintf(
		"SELECT id, name, device_id, device_model, device_os, app_version, ip, status, max_streams, expires_at, approved_by, reject_reason, last_seen, total_play_minutes, request_note, created_at, updated_at FROM clients WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?",
		where,
	)
	queryArgs := append(args, p.PageSize, offset)

	rows, err := s.db.Query(dataQuery, queryArgs...)
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

	return &models.PageResponse{Total: total, Page: p.Page, PageSize: p.PageSize, Items: clients}, nil
}
