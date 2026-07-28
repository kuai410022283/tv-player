package services

import (
	"database/sql"
	"fmt"
	"log/slog"
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
	db.SetMaxOpenConns(5) // SQLite 单写入者，配合 WAL 模式允许多个读取者并发
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
	_, _ = db.Exec("ALTER TABLE plan_group_relations ADD COLUMN sort_order INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN fcc TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN fcc_type TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN linked_channel_id INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN is_protected INTEGER DEFAULT 0;")
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN enable_multiplex INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN enable_multiplex INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE clients ADD COLUMN enable_log INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE subscription_plans ADD COLUMN subscription_token TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN content_type TEXT DEFAULT ''`)
	// 代理配置字段
	_, _ = db.Exec(`ALTER TABLE m3u_sources ADD COLUMN proxy_type TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE m3u_sources ADD COLUMN proxy_url TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN proxy_type TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channel_groups ADD COLUMN proxy_url TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN proxy_type TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN proxy_url TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE channels ADD COLUMN is_enabled INTEGER DEFAULT 1`)
	// Ensure fcc_type setting exists for existing databases
	_, _ = db.Exec(`INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_type', 'telecom')`)

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
				enable_multiplex INTEGER DEFAULT 0,
				proxy_type TEXT DEFAULT '',
				proxy_url TEXT DEFAULT '',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);`,
			`INSERT INTO channel_groups_new (id, name, icon, sort_order, is_direct, source, user_agent, custom_headers, enable_multiplex, proxy_type, proxy_url, created_at, updated_at)
		SELECT id, name, icon, sort_order, COALESCE(is_direct, 1), COALESCE(source, '手动'), COALESCE(user_agent, ''), COALESCE(custom_headers, ''), COALESCE(enable_multiplex, 0), COALESCE(proxy_type, ''), COALESCE(proxy_url, ''), created_at, updated_at FROM channel_groups;`,
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
		proxy_type TEXT DEFAULT '',
		proxy_url TEXT DEFAULT '',
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
		is_enabled INTEGER DEFAULT 1,
		is_direct INTEGER DEFAULT 1,
		sort_order INTEGER DEFAULT 0,
		status TEXT DEFAULT 'unknown',
		last_check DATETIME,
		m3u_source_id INTEGER DEFAULT 0,
		source TEXT DEFAULT '手动',
		user_agent TEXT DEFAULT '',
		custom_headers TEXT DEFAULT '',
		support_catchup INTEGER DEFAULT 1,
		catchup_type TEXT DEFAULT '',
		catchup_source TEXT DEFAULT '',
		catchup_days INTEGER DEFAULT 0,
		enable_multiplex INTEGER DEFAULT 0,
		fcc TEXT DEFAULT '',
		fcc_type TEXT DEFAULT '',
		proxy_type TEXT DEFAULT '',
		proxy_url TEXT DEFAULT '',
		linked_channel_id INTEGER DEFAULT 0,
		is_protected INTEGER DEFAULT 0,
		content_type TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE SET DEFAULT
	);

	-- 频道表索引：优化按分组查询和隐藏过滤
	CREATE INDEX IF NOT EXISTS idx_channels_group_id ON channels(group_id);
	CREATE INDEX IF NOT EXISTS idx_channels_is_hidden ON channels(is_hidden);
	CREATE INDEX IF NOT EXISTS idx_channels_group_hidden ON channels(group_id, is_hidden);
	CREATE INDEX IF NOT EXISTS idx_channels_is_enabled ON channels(is_enabled);
	CREATE INDEX IF NOT EXISTS idx_channels_group_enabled ON channels(group_id, is_enabled);
	CREATE INDEX IF NOT EXISTS idx_channels_sort_order ON channels(group_id, sort_order);

	-- 级联删除触发器
	CREATE TRIGGER IF NOT EXISTS trg_cascade_mirror_delete
	AFTER DELETE ON channels
	BEGIN
		DELETE FROM channels WHERE linked_channel_id = old.id;
	END;

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
		proxy_type TEXT DEFAULT '',
		proxy_url TEXT DEFAULT '',
		last_sync DATETIME,
		sync_status TEXT DEFAULT 'idle',
		sync_error TEXT DEFAULT '',
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
		status TEXT NOT NULL DEFAULT 'pending',
		plan_id INTEGER DEFAULT 0,
		max_streams INTEGER DEFAULT 2,
		expires_at DATETIME,
		approved_by TEXT DEFAULT '',
		reject_reason TEXT DEFAULT '',
		last_seen DATETIME,
		total_play_minutes INTEGER DEFAULT 0,
		request_note TEXT DEFAULT '',
		enable_log INTEGER DEFAULT 0,
		is_tester INTEGER DEFAULT 0,
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
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('server_name', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('enable_external_sub', 'false');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('logo_strategy', 'source');

	-- ── Sync settings ───────────────────────────────────
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('sync_enable', 'false');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('sync_master_url', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('sync_master_token', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('sync_serve_token', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('sync_interval_min', '5');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_enabled', 'false');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_port_start', '40000');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_port_end', '45000');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_default_server', '');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('fcc_type', 'telecom');
	INSERT OR IGNORE INTO user_settings (key, value) VALUES ('app_display_name', 'MediaPlayer');

	CREATE TABLE IF NOT EXISTS subscription_plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		days INTEGER DEFAULT 30,
		max_streams INTEGER DEFAULT 1,
		price REAL DEFAULT 0.0,
		description TEXT DEFAULT '',
		subscription_token TEXT DEFAULT '',
		enable_aggregation INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS plan_group_relations (
		plan_id INTEGER NOT NULL,
		group_id INTEGER NOT NULL,
		sort_order INTEGER DEFAULT 0,
		PRIMARY KEY (plan_id, group_id),
		FOREIGN KEY (plan_id) REFERENCES subscription_plans(id) ON DELETE CASCADE,
		FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE
	);

	INSERT INTO channel_groups (name, sort_order)
	SELECT '未分类', 99999
	WHERE NOT EXISTS (SELECT 1 FROM channel_groups WHERE name = '未分类');
	`

	// 逐条执行 schema 语句，确保每条 DDL 都被执行。
	// modernc.org/sqlite 的 db.Exec() 对多语句长字符串的处理存在边界情况，
	// 可能只执行第一条语句而静默跳过后续语句，导致表/索引/触发器未创建。
	// 拆分为逐条执行可彻底避免此问题。
	for _, stmt := range splitSQLStatements(schema) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			slog.Warn("schema statement failed (may be expected for existing DB)", "error", err, "stmt", truncate(stmt, 80))
		}
	}

	// ── 存量数据兜底：将所有未开启回看的频道默认视为支持回看 ──────────
	// 背景：旧版本导入时 support_catchup 默认值为 0，导致用户即使源地址
	// 支持回看也无法使用该功能。此迁移将所有 support_catchup=0 的频道
	// 统一修正为 1（乐观策略：允许尝试，不行则由后端报错而非客户端拦截）。
	_, _ = db.Exec(`UPDATE channels SET support_catchup = 1 WHERE support_catchup = 0 OR support_catchup IS NULL`)

	// 执行自动迁移
	_, _ = db.Exec("ALTER TABLE channel_groups ADD COLUMN is_direct INTEGER DEFAULT 1;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN is_direct INTEGER DEFAULT 1;")
	_, _ = db.Exec("ALTER TABLE clients ADD COLUMN plan_id INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN m3u_source_id INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE channels ADD COLUMN custom_headers TEXT DEFAULT '';")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN sync_interval INTEGER DEFAULT 12;")
	_, _ = db.Exec("ALTER TABLE clients ADD COLUMN is_tester INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE subscription_plans ADD COLUMN enable_aggregation INTEGER DEFAULT 0;")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN sync_status TEXT DEFAULT 'idle';")
	_, _ = db.Exec("ALTER TABLE m3u_sources ADD COLUMN sync_error TEXT DEFAULT '';")

	// 客户端远程配置表（新功能，自动幂等建表）
	MigrateClientConfig(db)

	return nil
}

// truncate 截断字符串用于日志输出
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// splitSQLStatements 按分号分割 SQL 语句，正确处理 BEGIN...END 块内的分号
func splitSQLStatements(schema string) []string {
	var result []string
	var current strings.Builder
	upper := strings.ToUpper(schema)
	inBlock := 0
	i := 0
	for i < len(schema) {
		// 处理 -- 行注释：跳过到行尾
		if i+1 < len(schema) && schema[i] == '-' && schema[i+1] == '-' {
			for i < len(schema) && schema[i] != '\n' {
				i++
			}
			continue
		}
		// 检测 BEGIN 关键字（触发器/事务块）
		if i+5 <= len(schema) && upper[i:i+5] == "BEGIN" &&
			(i == 0 || upper[i-1] == ' ' || upper[i-1] == '\n' || upper[i-1] == '\t') &&
			(i+5 >= len(upper) || upper[i+5] == ' ' || upper[i+5] == '\n' || upper[i+5] == '\t') {
			inBlock++
			current.WriteString(schema[i : i+5])
			i += 5
			continue
		}
		// 检测 END 关键字
		if i+3 <= len(schema) && upper[i:i+3] == "END" &&
			(i == 0 || upper[i-1] == ' ' || upper[i-1] == '\n' || upper[i-1] == '\t') &&
			(i+3 >= len(upper) || upper[i+3] == ' ' || upper[i+3] == '\n' || upper[i+3] == '\t' || upper[i+3] == ';') {
			if inBlock > 0 {
				inBlock--
			}
			current.WriteString(schema[i : i+3])
			i += 3
			continue
		}
		// 分号：在块内则属于当前语句，否则为语句分隔符
		if schema[i] == ';' && inBlock == 0 {
			result = append(result, current.String())
			current.Reset()
			i++
			continue
		}
		current.WriteByte(schema[i])
		i++
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		result = append(result, s)
	}
	return result
}
