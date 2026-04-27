package services

import (
	"database/sql"

	"github.com/tvplayer/backend/internal/models"
)

type EPGService struct {
	db *sql.DB
}

func NewEPGService(db *sql.DB) *EPGService {
	return &EPGService{db: db}
}

// GetByChannelID 查询指定频道的 EPG 节目单
func (s *EPGService) GetByChannelID(channelID string) ([]models.EPGProgram, error) {
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
	return items, nil
}

// Add 添加 EPG 节目
func (s *EPGService) Add(p *models.EPGProgram) error {
	res, err := s.db.Exec(`INSERT INTO epg_programs (epg_channel_id, title, start_time, end_time, description) VALUES (?,?,?,?,?)`,
		p.ChannelID, p.Title, p.StartTime, p.EndTime, p.Desc)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}
