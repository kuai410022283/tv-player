package services

import (
	"database/sql"
	"time"

	"github.com/tvplayer/backend/internal/models"
)

type PlanService struct {
	db *sql.DB
}

func NewPlanService(db *sql.DB) *PlanService {
	return &PlanService{db: db}
}

func (s *PlanService) GetPlans() ([]*models.SubscriptionPlan, error) {
	rows, err := s.db.Query(`SELECT id, name, days, max_streams, price, description, created_at, updated_at FROM subscription_plans ORDER BY id ASC`)
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
	return items, rows.Err()
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
