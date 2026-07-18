package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

type PlanService struct {
	db      *sql.DB
	logoSvc *LogoService
}

func NewPlanService(db *sql.DB, logoSvc *LogoService) *PlanService {
	return &PlanService{db: db, logoSvc: logoSvc}
}

func (s *PlanService) GetLogoService() *LogoService {
	return s.logoSvc
}

func (s *PlanService) GetEPGSourceURL() string {
	var urlStr string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_source_url'`).Scan(&urlStr)
	if err != nil {
		return ""
	}
	return urlStr
}

func (s *PlanService) GetEPGTimeShift() int {
	var shiftStr string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='epg_time_shift'`).Scan(&shiftStr)
	if err != nil {
		return 0
	}
	shift, _ := strconv.Atoi(shiftStr)
	return shift
}

func (s *PlanService) GetPlans(search string) ([]*models.SubscriptionPlan, error) {
	query := `SELECT id, name, days, max_streams, price, description, subscription_token, enable_aggregation, created_at, updated_at FROM subscription_plans`
	var args []interface{}
	if search != "" {
		query += ` WHERE name LIKE ?`
		args = append(args, "%"+search+"%")
	}
	query += ` ORDER BY id ASC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*models.SubscriptionPlan
	for rows.Next() {
		m := &models.SubscriptionPlan{}
		if err := rows.Scan(&m.ID, &m.Name, &m.Days, &m.MaxStreams, &m.Price, &m.Description, &m.SubscriptionToken, &m.EnableAggregation, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}

	// Fetch group associations (ordered by sort_order)
	for _, m := range items {
		m.GroupIDs = make([]int64, 0)
		gRows, err := s.db.Query(`SELECT group_id FROM plan_group_relations WHERE plan_id=? ORDER BY sort_order ASC, group_id ASC`, m.ID)
		if err == nil {
			for gRows.Next() {
				var gID int64
				if gRows.Scan(&gID) == nil {
					m.GroupIDs = append(m.GroupIDs, gID)
				}
			}
			_ = gRows.Close()
		}
	}

	return items, nil
}

func (s *PlanService) AddPlan(m *models.SubscriptionPlan) error {
	now := time.Now()
	if m.SubscriptionToken == "" {
		m.SubscriptionToken = generateToken()
	}
	res, err := s.db.Exec(`INSERT INTO subscription_plans (name, days, max_streams, price, description, subscription_token, enable_aggregation, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)`,
		m.Name, m.Days, m.MaxStreams, m.Price, m.Description, m.SubscriptionToken, m.EnableAggregation, now, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	m.UpdatedAt = now

	// Save group relations with sort order
	for i, gID := range m.GroupIDs {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO plan_group_relations (plan_id, group_id, sort_order) VALUES (?,?,?)`, m.ID, gID, i)
	}

	return nil
}

func (s *PlanService) UpdatePlan(m *models.SubscriptionPlan) error {
	now := time.Now()
	if m.SubscriptionToken == "" {
		m.SubscriptionToken = generateToken()
	}
	_, err := s.db.Exec(`UPDATE subscription_plans SET name=?, days=?, max_streams=?, price=?, description=?, subscription_token=?, enable_aggregation=?, updated_at=? WHERE id=?`,
		m.Name, m.Days, m.MaxStreams, m.Price, m.Description, m.SubscriptionToken, m.EnableAggregation, now, m.ID)
	if err != nil {
		return err
	}
	m.UpdatedAt = now

	// Update group relations with sort order
	_, _ = s.db.Exec(`DELETE FROM plan_group_relations WHERE plan_id=?`, m.ID)
	for i, gID := range m.GroupIDs {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO plan_group_relations (plan_id, group_id, sort_order) VALUES (?,?,?)`, m.ID, gID, i)
	}

	return nil
}

func (s *PlanService) DeletePlan(id int64) error {
	// Optional: You could update clients using this plan to set plan_id = 0
	_, err := s.db.Exec(`DELETE FROM subscription_plans WHERE id=?`, id)
	if err == nil {
		_, _ = s.db.Exec(`UPDATE clients SET plan_id=0 WHERE plan_id=?`, id)
	}
	return err
}

func (s *PlanService) GetSubscriptionChannels(planName, token string) ([]*models.SubscriptionChannel, error) {
	// 1. 验证套餐及订阅 Token 匹配度
	var planID int64
	var enableAggregation int
	err := s.db.QueryRow(`SELECT id, enable_aggregation FROM subscription_plans WHERE name=? AND subscription_token=?`, planName, token).Scan(&planID, &enableAggregation)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("invalid plan or token")
		}
		return nil, err
	}

	// 2. 加载套餐关联分组下的非隐藏频道 (并按套餐分组排序和频道排序条件排序)
	query := `
		SELECT c.id, c.group_id, cg.name AS group_name, COALESCE(cg.source, '') AS group_source, c.name, COALESCE(c.logo, '') AS logo, 
		       c.stream_url, COALESCE(c.stream_type, '') AS stream_type, COALESCE(c.epg_channel_id, '') AS epg_channel_id,
		       c.is_direct, c.support_catchup, COALESCE(c.catchup_type, '') AS catchup_type, COALESCE(c.catchup_source, '') AS catchup_source, c.catchup_days,
		       COALESCE(c.user_agent, '') AS user_agent, COALESCE(c.custom_headers, '') AS custom_headers, COALESCE(c.content_type, '') AS content_type,
		       COALESCE(c.proxy_type, '') AS proxy_type, COALESCE(c.proxy_url, '') AS proxy_url
		FROM channels c
		JOIN channel_groups cg ON c.group_id = cg.id
		JOIN plan_group_relations pgr ON c.group_id = pgr.group_id
		WHERE pgr.plan_id = ? AND c.is_hidden = 0
		ORDER BY pgr.sort_order ASC, cg.id ASC, c.sort_order ASC, c.id ASC
	`
	rows, err := s.db.Query(query, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []*models.SubscriptionChannel
	for rows.Next() {
		m := &models.SubscriptionChannel{}
		var isDirect, supportCatchup int
		var groupSource string
		if err := rows.Scan(&m.ID, &m.GroupID, &m.GroupName, &groupSource, &m.Name, &m.Logo,
			&m.StreamURL, &m.StreamType, &m.EPGChannelID, &isDirect, &supportCatchup, &m.CatchupType, &m.CatchupSource, &m.CatchupDays, &m.UserAgent, &m.CustomHeaders, &m.ContentType, &m.ProxyType, &m.ProxyURL); err != nil {
			return nil, err
		}
		if enableAggregation == 0 && groupSource != "" && groupSource != "手动" {
			m.GroupName = fmt.Sprintf("%s(%s)", m.GroupName, groupSource)
		}
		if m.StreamType == "" {
			m.StreamType = "ts"
		}
		m.IsDirect = isDirect == 1
		m.SupportCatchup = supportCatchup == 1
		items = append(items, m)
	}
	return items, nil
}

func (s *PlanService) GetServerURL() string {
	var val string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='server_url'`).Scan(&val)
	if err != nil {
		return ""
	}
	return val
}

func (s *PlanService) IsExternalSubEnabled() bool {
	var val string
	err := s.db.QueryRow(`SELECT value FROM user_settings WHERE key='enable_external_sub'`).Scan(&val)
	if err != nil {
		return false
	}
	return val == "true"
}
