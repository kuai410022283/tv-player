package services

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// 连接池配置（SQLite 单文件，限制并发写入）
	db.SetMaxOpenConns(5) // SQLite 单写入者
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(0) // 不复用连接（SQLite 文件句柄）

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS channel_groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		icon TEXT DEFAULT '',
		sort_order INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NOT NULL DEFAULT 0,
		name TEXT NOT NULL,
		logo TEXT DEFAULT '',
		description TEXT DEFAULT '',
		stream_url TEXT NOT NULL,
		stream_type TEXT NOT NULL DEFAULT 'hls',
		epg_channel_id TEXT DEFAULT '',
		is_favorite INTEGER DEFAULT 0,
		is_hidden INTEGER DEFAULT 0,
		sort_order INTEGER DEFAULT 0,
		status TEXT DEFAULT 'unknown',
		last_check DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE SET DEFAULT
	);

	CREATE TABLE IF NOT EXISTS epg_programs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		epg_channel_id TEXT NOT NULL,
		title TEXT NOT NULL,
		start_time DATETIME NOT NULL,
		end_time DATETIME NOT NULL,
		description TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_epg_channel_time ON epg_programs(epg_channel_id, start_time);

	CREATE TABLE IF NOT EXISTS play_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		channel_id INTEGER NOT NULL,
		client_id INTEGER DEFAULT 0,
		duration INTEGER DEFAULT 0,
		last_pos INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS user_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS m3u_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		auto_sync INTEGER DEFAULT 0,
		last_sync DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- ── Client Authorization ────────────────────────────

	CREATE TABLE IF NOT EXISTS clients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL DEFAULT '',
		device_id TEXT NOT NULL UNIQUE,
		device_model TEXT DEFAULT '',
		device_os TEXT DEFAULT '',
		app_version TEXT DEFAULT '',
		ip TEXT DEFAULT '',
		access_token TEXT UNIQUE,
		status TEXT NOT NULL DEFAULT 'pending',  -- pending / approved / rejected / banned / expired
		max_streams INTEGER DEFAULT 2,
		expires_at DATETIME,
		approved_by TEXT DEFAULT '',
		reject_reason TEXT DEFAULT '',
		last_seen DATETIME,
		total_play_minutes INTEGER DEFAULT 0,
		request_note TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_clients_status ON clients(status);
	CREATE INDEX IF NOT EXISTS idx_clients_device ON clients(device_id);
	CREATE INDEX IF NOT EXISTS idx_clients_token ON clients(access_token);

	CREATE TABLE IF NOT EXISTS access_logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		client_id INTEGER NOT NULL,
		action TEXT NOT NULL,
		channel_id INTEGER,
		ip TEXT DEFAULT '',
		user_agent TEXT DEFAULT '',
		detail TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_access_logs_client ON access_logs(client_id, created_at);

	CREATE TABLE IF NOT EXISTS licenses (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		license_key TEXT NOT NULL UNIQUE,
		client_id INTEGER,
		max_devices INTEGER DEFAULT 1,
		max_streams INTEGER DEFAULT 2,
		features TEXT DEFAULT '[]',
		expires_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE SET NULL
	);

	-- ── Auto-reject policy setting ──────────────────────
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('auto_approve', 'false');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('default_max_streams', '2');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('default_expire_days', '365');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('require_note', 'false');

	-- Insert default groups
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('央视', 1);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('卫视', 2);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('地方台', 3);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('体育', 4);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('影视', 5);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('综艺', 6);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('新闻', 7);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('少儿', 8);
	INSERT OR IGNORE INTO channel_groups (name, sort_order) VALUES ('未分类', 99);
	`

	_, err := db.Exec(schema)
	return err
}
