package services

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

// ClientConfigService 管理客户端远程配置的读写
type ClientConfigService struct {
	db *sql.DB
}

func NewClientConfigService(db *sql.DB) *ClientConfigService {
	return &ClientConfigService{db: db}
}

// ── 数据库迁移（由 InitDB 调用）────────────────────────

// MigrateClientConfig 建表（幂等）
func MigrateClientConfig(db *sql.DB) {
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS client_remote_configs (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		scope      TEXT NOT NULL DEFAULT 'global',
		config_key TEXT NOT NULL,
		config_val TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(scope, config_key)
	)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_client_configs_scope ON client_remote_configs(scope)`)
	// 迁移：添加 hidden 列（幂等）
	_, _ = db.Exec(`ALTER TABLE client_remote_configs ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`)
}

// ── 全局配置 ───────────────────────────────────────────

// GetGlobalConfigs 获取所有全局配置条目
func (s *ClientConfigService) GetGlobalConfigs() ([]models.ClientConfigEntry, error) {
	return s.getConfigsByScope("global")
}

// SaveGlobalConfigs 批量保存全局配置（UPSERT，值为空字符串表示删除管控）
func (s *ClientConfigService) SaveGlobalConfigs(configs map[string]interface{}, hidden map[string]bool) error {
	return s.saveConfigs("global", configs, hidden)
}

// ── 单客户端配置 ───────────────────────────────────────

// GetClientConfigs 获取单个设备的配置条目
func (s *ClientConfigService) GetClientConfigs(clientID int64) ([]models.ClientConfigEntry, error) {
	return s.getConfigsByScope(clientScope(clientID))
}

// SaveClientConfigs 批量保存单设备配置
func (s *ClientConfigService) SaveClientConfigs(clientID int64, configs map[string]interface{}, hidden map[string]bool) error {
	return s.saveConfigs(clientScope(clientID), configs, hidden)
}

// DeleteClientConfig 删除单设备的某个配置项（恢复为全局值）
func (s *ClientConfigService) DeleteClientConfig(clientID int64, key string) error {
	_, err := s.db.Exec(
		`DELETE FROM client_remote_configs WHERE scope=? AND config_key=?`,
		clientScope(clientID), key,
	)
	return err
}

// DeleteAllClientConfigs 删除单设备的所有配置（恢复全部为全局值）
func (s *ClientConfigService) DeleteAllClientConfigs(clientID int64) error {
	_, err := s.db.Exec(
		`DELETE FROM client_remote_configs WHERE scope=?`,
		clientScope(clientID),
	)
	return err
}

// ── 有效配置（合并全局+单设备）──────────────────────────

// GetEffectiveConfig 合并全局配置与单设备覆盖，构建 ClientRemoteConfig。
// 单设备配置优先；某项值为空字符串时视为不管控，对应字段保持 nil。
func (s *ClientConfigService) GetEffectiveConfig(clientID int64) (*models.ClientRemoteConfig, error) {
	// 查全局
	globalRows, err := s.getConfigsByScope("global")
	if err != nil {
		return nil, err
	}
	merged := make(map[string]string)
	// 收集全局隐藏项
	globalHidden := make(map[string]bool)
	for _, r := range globalRows {
		if r.ConfigVal != "" {
			merged[r.ConfigKey] = r.ConfigVal
		}
		if r.Hidden != 0 {
			globalHidden[r.ConfigKey] = true
		}
	}

	// 查单设备（覆盖全局）
	clientRows, err := s.getConfigsByScope(clientScope(clientID))
	if err != nil {
		return nil, err
	}
	clientHidden := make(map[string]bool)
	for _, r := range clientRows {
		if r.ConfigVal != "" {
			merged[r.ConfigKey] = r.ConfigVal
		} else if r.Hidden == 0 {
			// 空值且不隐藏 = 取消对该项的管控（删除全局管控）
			delete(merged, r.ConfigKey)
		}
		// else: 空值但隐藏 = 保留全局值，仅控制隐藏
		if r.Hidden != 0 {
			clientHidden[r.ConfigKey] = true
		} else {
			// 单设备明确不隐藏 = 覆盖全局隐藏
			delete(clientHidden, r.ConfigKey)
		}
	}

	cfg := buildRemoteConfig(merged)
	if cfg == nil {
		return nil, nil
	}

	// 合并隐藏键：全局隐藏 + 单设备隐藏
	hiddenSet := make(map[string]bool)
	for k := range globalHidden {
		hiddenSet[k] = true
	}
	for k := range clientHidden {
		hiddenSet[k] = true
	}
	// 单设备明确不隐藏的要从集合中移除
	for _, r := range clientRows {
		if r.Hidden == 0 {
			delete(hiddenSet, r.ConfigKey)
		}
	}
	for k := range hiddenSet {
		cfg.HiddenKeys = append(cfg.HiddenKeys, k)
	}

	return cfg, nil
}

// ── 内部辅助 ───────────────────────────────────────────

func (s *ClientConfigService) getConfigsByScope(scope string) ([]models.ClientConfigEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, scope, config_key, config_val, hidden, updated_at FROM client_remote_configs WHERE scope=? ORDER BY config_key`,
		scope,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	entries := make([]models.ClientConfigEntry, 0)
	for rows.Next() {
		var e models.ClientConfigEntry
		var updatedAt time.Time
		if err := rows.Scan(&e.ID, &e.Scope, &e.ConfigKey, &e.ConfigVal, &e.Hidden, &updatedAt); err != nil {
			return nil, err
		}
		e.UpdatedAt = updatedAt.Format(time.RFC3339)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *ClientConfigService) saveConfigs(scope string, configs map[string]interface{}, hidden map[string]bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()
	for key, val := range configs {
		if !isValidConfigKey(key) {
			continue // 忽略无效键
		}
		hiddenVal := 0
		if hidden != nil && hidden[key] {
			hiddenVal = 1
		}
		if val == nil && hiddenVal == 0 {
			// 不管控且不隐藏 → 删除整行
			_, err = tx.Exec(`DELETE FROM client_remote_configs WHERE scope=? AND config_key=?`, scope, key)
		} else if val == nil && hiddenVal == 1 {
			// 仅隐藏，不管控值 → 只更新 hidden 标志，保留 config_val 为空（不覆盖全局值）
			_, err = tx.Exec(
				`INSERT INTO client_remote_configs (scope, config_key, config_val, hidden, updated_at)
				 VALUES (?, ?, '', ?, ?)
				 ON CONFLICT(scope, config_key) DO UPDATE SET hidden=excluded.hidden, updated_at=excluded.updated_at`,
				scope, key, hiddenVal, now,
			)
		} else if valStr, ok := val.(string); ok && valStr == "" && hiddenVal == 0 {
			// 空字符串等价于"不管控"（兼容前端可能传空字符串的安全兜底）
			_, err = tx.Exec(`DELETE FROM client_remote_configs WHERE scope=? AND config_key=?`, scope, key)
		} else {
			// 有管控值 或 仅隐藏 → INSERT/UPDATE
			valStr := ""
			if val != nil {
				valStr = fmt.Sprintf("%v", val)
			}
			_, err = tx.Exec(
				`INSERT INTO client_remote_configs (scope, config_key, config_val, hidden, updated_at)
				 VALUES (?, ?, ?, ?, ?)
				 ON CONFLICT(scope, config_key) DO UPDATE SET config_val=excluded.config_val, hidden=excluded.hidden, updated_at=excluded.updated_at`,
				scope, key, valStr, hiddenVal, now,
			)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func clientScope(clientID int64) string {
	return fmt.Sprintf("client:%d", clientID)
}

// isValidConfigKey 白名单校验，防止任意键注入
func isValidConfigKey(key string) bool {
	validKeys := map[string]bool{
		"player_core": true, "decoder_mode": true, "network_cache_ms": true,
		"audio_passthrough": true, "audio_normalizer_enabled": true, "scale_mode": true, "dns_policy": true,
		"stop_previous_media": true, "show_channel_logo": true,
		"show_group_source": true, "global_progress_bar": true,
		"time_show_mode": true, "control_scheme": true, "auto_start": true,
		"enable_pip": true, "local_proxy_enabled": true,
		"gesture_brightness": true, "gesture_volume": true,
		"reverse_channel_keys": true,
		"hide_settings_panel":  true, "hide_channel_list": true,
		"hide_epg_panel": true, "hide_osd_panel": true,
		"hide_community": true, "hide_qr_config": true,
		"preferred_server_index": true, "auto_check_update": true, "check_update": true,
		"app_language": true,
	}
	return validKeys[key]
}

// buildRemoteConfig 将 map[string]string 转换为 ClientRemoteConfig（带类型转换）
func buildRemoteConfig(m map[string]string) *models.ClientRemoteConfig {
	if len(m) == 0 {
		return nil
	}
	cfg := &models.ClientRemoteConfig{}
	hasAny := false

	setInt := func(key string, ptr **int) {
		if v, ok := m[key]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				*ptr = &n
				hasAny = true
			} else if v == "true" {
				n := 0
				*ptr = &n
				hasAny = true
			} else if v == "false" {
				n := -1
				*ptr = &n
				hasAny = true
			}
		}
	}
	setBool := func(key string, ptr **bool) {
		if v, ok := m[key]; ok {
			b := strings.ToLower(v) == "true" || v == "1"
			*ptr = &b
			hasAny = true
		}
	}

	setInt("player_core", &cfg.PlayerCore)
	setInt("decoder_mode", &cfg.DecoderMode)
	setInt("network_cache_ms", &cfg.NetworkCacheMs)
	setBool("audio_passthrough", &cfg.AudioPassthrough)
	setBool("audio_normalizer_enabled", &cfg.AudioNormalizer)
	setInt("scale_mode", &cfg.ScaleMode)
	setInt("dns_policy", &cfg.DnsPolicy)
	setBool("stop_previous_media", &cfg.StopPreviousMedia)
	setBool("show_channel_logo", &cfg.ShowChannelLogo)
	setBool("show_group_source", &cfg.ShowGroupSource)
	setInt("global_progress_bar", &cfg.GlobalProgressBar)
	setInt("time_show_mode", &cfg.TimeShowMode)
	setInt("control_scheme", &cfg.ControlScheme)
	setBool("auto_start", &cfg.AutoStart)
	setBool("enable_pip", &cfg.EnablePip)
	setBool("local_proxy_enabled", &cfg.LocalProxyEnabled)
	setBool("gesture_brightness", &cfg.GestureBrightness)
	setBool("gesture_volume", &cfg.GestureVolume)
	setBool("reverse_channel_keys", &cfg.ReverseChannelKeys)
	setBool("hide_settings_panel", &cfg.HideSettingsPanel)
	setBool("hide_channel_list", &cfg.HideChannelList)
	setBool("hide_epg_panel", &cfg.HideEpgPanel)
	setBool("hide_osd_panel", &cfg.HideOsdPanel)
	setBool("hide_community", &cfg.HideCommunity)
	setBool("hide_qr_config", &cfg.HideQrConfig)
	setInt("preferred_server_index", &cfg.PreferredServerIndex)
	setBool("auto_check_update", &cfg.AutoCheckUpdate)
	setInt("app_language", &cfg.AppLanguage)

	if !hasAny {
		return nil
	}
	return cfg
}
