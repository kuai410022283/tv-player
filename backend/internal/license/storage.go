// Package license 提供 VIP 授权订阅功能。

package license

import (
	"database/sql"
	"log/slog"
	"time"
)

// ── SQLite 存储实现 ────────────────────────────────────

// SQLiteStorage 使用 SQLite 存储授权信息
type SQLiteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage 创建 SQLite 存储实例
func NewSQLiteStorage(db *sql.DB) *SQLiteStorage {
	return &SQLiteStorage{db: db}
}

// Migrate 建表（幂等）
func (s *SQLiteStorage) Migrate() {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS vip_license (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		license_key  TEXT NOT NULL,
		machine_id   TEXT NOT NULL,
		features     TEXT NOT NULL DEFAULT '',
		seq          TEXT NOT NULL UNIQUE,
		status       TEXT NOT NULL DEFAULT 'activated',
		expires_at   TEXT DEFAULT '',
		activated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_verified_at TEXT DEFAULT ''
	)`)
	if err != nil {
		slog.Warn("license: migrate table failed", "error", err)
	}
	// 动态为已有的数据库表添加 last_verified_at 字段
	_, _ = s.db.Exec(`ALTER TABLE vip_license ADD COLUMN last_verified_at TEXT DEFAULT ''`)
}

// UpdateLastVerifiedAt 更新最后校验通过的时间（加密密文）
func (s *SQLiteStorage) UpdateLastVerifiedAt(encryptedTime string) error {
	_, err := s.db.Exec(`UPDATE vip_license SET last_verified_at=?, updated_at=CURRENT_TIMESTAMP WHERE status='activated'`)
	return err
}

// GetLastVerifiedAt 获取最后校验通过的时间（加密密文）
func (s *SQLiteStorage) GetLastVerifiedAt() (string, error) {
	var lastVerified string
	err := s.db.QueryRow(`SELECT coalesce(last_verified_at, '') FROM vip_license WHERE status='activated' ORDER BY id DESC LIMIT 1`).Scan(&lastVerified)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return lastVerified, err
}

func (s *SQLiteStorage) Load() (licenseKey, machineID, features, expiresAt, seq, activatedAt string, err error) {
	row := s.db.QueryRow(`SELECT license_key, machine_id, features, expires_at, seq, activated_at FROM vip_license WHERE status='activated' ORDER BY id DESC LIMIT 1`)
	err = row.Scan(&licenseKey, &machineID, &features, &expiresAt, &seq, &activatedAt)
	if err == sql.ErrNoRows {
		return "", "", "", "", "", "", nil
	}
	return
}

func (s *SQLiteStorage) Save(licenseKey, machineID, features, expiresAt, seq string) error {
	// 在事务中完成：先吊销当前激活记录，再删除同序列号的旧记录，最后插入新记录
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// 吊销当前激活记录
	_, _ = tx.Exec(`UPDATE vip_license SET status='revoked', updated_at=CURRENT_TIMESTAMP WHERE status='activated'`)

	// 删除同序列号的历史记录（同机器重复激活时避免 UNIQUE 约束冲突）
	_, _ = tx.Exec(`DELETE FROM vip_license WHERE seq=?`, seq)

	// 插入新激活记录
	_, err = tx.Exec(
		`INSERT INTO vip_license (license_key, machine_id, features, seq, status, expires_at, activated_at) VALUES (?, ?, ?, ?, 'activated', ?, CURRENT_TIMESTAMP)`,
		licenseKey, machineID, features, seq, expiresAt,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStorage) Delete() error {
	_, err := s.db.Exec(`UPDATE vip_license SET status='revoked', updated_at=CURRENT_TIMESTAMP WHERE status='activated'`)
	return err
}

// SeqExists 检查序列号是否已在其他机器上使用过。
// 同一机器码允许重复激活（例如重装软件后重新激活）。
func (s *SQLiteStorage) SeqExists(seq, machineID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM vip_license WHERE seq=? AND machine_id != ?`, seq, machineID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ── 清理过期授权（后台任务用）──────────────────────────

// CleanupExpired 清理过期的授权记录
func (s *SQLiteStorage) CleanupExpired() {
	_, err := s.db.Exec(`UPDATE vip_license SET status='expired', updated_at=CURRENT_TIMESTAMP WHERE status='activated' AND expires_at != '' AND expires_at != 'permanent' AND datetime(expires_at) < datetime('now', 'start of day')`)
	if err != nil {
		slog.Warn("license: cleanup expired failed", "error", err)
		return
	}

	// 清理 30 天前的 revoked 记录
	_, _ = s.db.Exec(`DELETE FROM vip_license WHERE status='revoked' AND created_at < datetime('now', '-30 days')`)
}

// StartCleanupTask 启动定时清理任务
func (s *SQLiteStorage) StartCleanupTask(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.CleanupExpired()
		}
	}
}
