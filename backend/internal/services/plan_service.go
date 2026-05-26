package services

import (
	"database/sql"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

type PlanService struct {
	db *sql.DB
}

func NewPlanService(db *sql.DB) *PlanService {
	return &PlanService{db: db}
}

func (s *PlanService) GetPlans(search string) ([]*models.SubscriptionPlan, error) {
	query := `SELECT id, name, days, max_streams, price, description, created_at, updated_at FROM subscription_plans`
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
		if err := rows.Scan(&m.ID, &m.Name, &m.Days, &m.MaxStreams, &m.Price, &m.Description, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, m)
	}

	// Fetch group associations
	for _, m := range items {
		m.GroupIDs = make([]int64, 0)
		gRows, err := s.db.Query(`SELECT group_id FROM plan_group_relations WHERE plan_id=?`, m.ID)
		if err == nil {
			for gRows.Next() {
				var gID int64
				if gRows.Scan(&gID) == nil {
					m.GroupIDs = append(m.GroupIDs, gID)
				}
			}
			gRows.Close()
		}
	}

	return items, nil
}

func (s *PlanService) AddPlan(m *models.SubscriptionPlan) error {
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO subscription_plans (name, days, max_streams, price, description, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		m.Name, m.Days, m.MaxStreams, m.Price, m.Description, now, now)
	if err != nil {
		return err
	}
	m.ID, _ = res.LastInsertId()
	m.CreatedAt = now
	m.UpdatedAt = now

	// Save group relations
	for _, gID := range m.GroupIDs {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO plan_group_relations (plan_id, group_id) VALUES (?,?)`, m.ID, gID)
	}

	return nil
}

func (s *PlanService) UpdatePlan(m *models.SubscriptionPlan) error {
	now := time.Now()
	_, err := s.db.Exec(`UPDATE subscription_plans SET name=?, days=?, max_streams=?, price=?, description=?, updated_at=? WHERE id=?`,
		m.Name, m.Days, m.MaxStreams, m.Price, m.Description, now, m.ID)
	if err != nil {
		return err
	}
	m.UpdatedAt = now

	// Update group relations
	_, _ = s.db.Exec(`DELETE FROM plan_group_relations WHERE plan_id=?`, m.ID)
	for _, gID := range m.GroupIDs {
		_, _ = s.db.Exec(`INSERT OR IGNORE INTO plan_group_relations (plan_id, group_id) VALUES (?,?)`, m.ID, gID)
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
