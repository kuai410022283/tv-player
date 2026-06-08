package services

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

func InitDB(dbPath string) (*sql.DB, error) {
	var dsn string
	if dbPath == ":memory:" {
		// modernc.org/sqlite 内存数据库
		dsn = "file::memory:?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	} else {
		// 必须使用 _pragma=name(value) 格式，否则现代纯 Go 驱动不会生效
		dsn = dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// 显式执行 PRAGMA，确保 WAL 和 Busy Timeout 绝对生效，防止 database is locked
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA busy_timeout=5000;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")

	// 连接池配置（SQLite 单文件，限制并发写入）
	db.SetMaxOpenConns(5) // SQLite 单写入者
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(0) // 不复用连接（SQLite 文件句柄）

	// 自动迁移字段
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN source TEXT DEFAULT '手动'`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN source TEXT DEFAULT '手动'`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN user_agent TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN custom_headers TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN user_agent TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE m3u_sources ADD COLUMN user_agent TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE m3u_sources ADD COLUMN custom_headers TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN support_catchup INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN catchup_type TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN catchup_source TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN catchup_days INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN enable_multiplex INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN enable_multiplex INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE clients ADD COLUMN enable_log INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE subscription_plans ADD COLUMN subscription_token TEXT DEFAULT ''`)

	// 自动为已有空 Token 的套餐生成 Token 凭证
	if rows, err := db.Query("SELECT id FROM subscription_plans WHERE subscription_token = '' OR subscription_token IS NULL"); err == nil {
		type planIDAndToken struct {
			id    int64
			token string
		}
		var updates []planIDAndToken
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				updates = append(updates, planIDAndToken{id: id, token: generateToken()})
			}
		}
		rows.Close()
		for _, u := range updates {
			_, _ = db.Exec("UPDATE subscription_plans SET subscription_token = ? WHERE id = ?", u.token, u.id)
		}
	}

	// 移除 channel_groups.name 的 UNIQUE 约束
	var sqlStmt string
	err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='channel_groups'").Scan(&sqlStmt)
	if err == nil && strings.Contains(sqlStmt, "UNIQUE") {
		queries := []string{
			"PRAGMA foreign_keys=off;",
			`CREATE TABLE channel_groups_new (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL,
				icon TEXT DEFAULT '',
				sort_order INTEGER DEFAULT 0,
				is_direct INTEGER DEFAULT 1,
				source TEXT DEFAULT '手动',
				user_agent TEXT DEFAULT '',
				custom_headers TEXT DEFAULT '',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);`,
			`INSERT INTO channel_groups_new (id, name, icon, sort_order, source, created_at, updated_at)
			SELECT id, name, icon, sort_order, COALESCE(source, '手动'), created_at, updated_at FROM channel_groups;`,
			"DROP TABLE channel_groups;",
			"ALTER TABLE channel_groups_new RENAME TO channel_groups;",
			"PRAGMA foreign_keys=on;",
		}
		
		for _, q := range queries {
			if _, errMigrate := db.Exec(q); errMigrate != nil {
				fmt.Println("Warning: channel_groups migration failed on query:", q, "Error:", errMigrate)
				break
			}
		}
	}

	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %w", err)
	}

	return db, nil
}

func createTables(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS channel_groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		icon TEXT DEFAULT '',
		sort_order INTEGER DEFAULT 0,
		is_direct INTEGER DEFAULT 1,
		source TEXT DEFAULT '手动',
		user_agent TEXT DEFAULT '',
		custom_headers TEXT DEFAULT '',
		enable_multiplex INTEGER DEFAULT 0,
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
		is_hidden INTEGER DEFAULT 0,
		is_direct INTEGER DEFAULT 1,
		sort_order INTEGER DEFAULT 0,
		status TEXT DEFAULT 'unknown',
		last_check DATETIME,
		m3u_source_id INTEGER DEFAULT 0,
		source TEXT DEFAULT '手动',
		user_agent TEXT DEFAULT '',
		custom_headers TEXT DEFAULT '',
		support_catchup INTEGER DEFAULT 0,
		catchup_type TEXT DEFAULT '',
		catchup_source TEXT DEFAULT '',
		catchup_days INTEGER DEFAULT 0,
		enable_multiplex INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE SET DEFAULT
	);

	DROP TABLE IF EXISTS epg_programs;

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

	DROP TABLE IF EXISTS client_channel_favorites;

	CREATE TABLE IF NOT EXISTS m3u_sources (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		auto_sync INTEGER DEFAULT 0,
		sync_interval INTEGER DEFAULT 12,
		user_agent TEXT DEFAULT '',
		custom_headers TEXT DEFAULT '',
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
		plan_id INTEGER DEFAULT 0,
		max_streams INTEGER DEFAULT 2,
		expires_at DATETIME,
		approved_by TEXT DEFAULT '',
		reject_reason TEXT DEFAULT '',
		last_seen DATETIME,
		total_play_minutes INTEGER DEFAULT 0,
		request_note TEXT DEFAULT '',
		enable_log INTEGER DEFAULT 0,
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
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('server_url', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('enable_external_sub', 'false');

	CREATE TABLE IF NOT EXISTS subscription_plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		days INTEGER DEFAULT 30,
		max_streams INTEGER DEFAULT 1,
		price REAL DEFAULT 0.0,
		description TEXT DEFAULT '',
		subscription_token TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS plan_group_relations (
		plan_id INTEGER NOT NULL,
		group_id INTEGER NOT NULL,
		PRIMARY KEY (plan_id, group_id),
		FOREIGN KEY (plan_id) REFERENCES subscription_plans(id) ON DELETE CASCADE,
		FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE
	);

	INSERT INTO channel_groups (name, sort_order)
	SELECT '未分类', 99999
	WHERE NOT EXISTS (SELECT 1 FROM channel_groups WHERE name = '未分类');
	`

	_, err := db.Exec(schema)

	// 执行自动迁移
	_, _ = db.Exec("ALTER TABLE channel_groups ADD COLUMN is_direct INTEGER DEFAULT 1;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN is_direct INTEGER DEFAULT 1;")
	_, _ = db.Exec("ALTER TABLE clients ADD COLUMN plan_id INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN m3u_source_id INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN custom_headers TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN sync_interval INTEGER DEFAULT 12;")
	_, _ = db.Exec("ALTER TABLE channel_groups ADD COLUMN user_agent TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channel_groups ADD COLUMN custom_headers TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN user_agent TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN user_agent TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN custom_headers TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN support_catchup INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN catchup_type TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN catchup_source TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN catchup_days INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN enable_multiplex INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channel_groups ADD COLUMN enable_multiplex INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE clients ADD COLUMN enable_log INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE subscription_plans ADD COLUMN subscription_token TEXT DEFAULT '';")

	return err
}
