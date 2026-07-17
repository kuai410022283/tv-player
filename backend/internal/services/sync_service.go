package services

import (
	"context"
	"crypto/md5"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type SyncService struct {
	db *sql.DB
}

func NewSyncService(db *sql.DB) *SyncService {
	return &SyncService{db: db}
}

// Snapshot exports the current database safely to a given path using VACUUM INTO.
func (s *SyncService) Snapshot(outputPath string) error {
	// Ensure the directory exists
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for snapshot: %w", err)
	}

	// Remove old backup if exists
	if _, err := os.Stat(outputPath); err == nil {
		_ = os.Remove(outputPath)
	}

	// VACUUM INTO performs an online backup safely.
	_, err := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", outputPath))
	if err != nil {
		return fmt.Errorf("VACUUM INTO failed: %w", err)
	}

	return nil
}

// SyncFromMaster downloads a snapshot from the master node and applies it locally using ATTACH DATABASE.
func (s *SyncService) SyncFromMaster(masterURL, masterToken string) error {
	if masterURL == "" {
		return fmt.Errorf("master URL is empty")
	}

	downloadPath := filepath.Join("data", fmt.Sprintf("master_sync_%d.db", time.Now().UnixNano()))
	
	// Ensure data dir exists
	_ = os.MkdirAll("data", 0755)

	// 1. Download snapshot
	apiURL := fmt.Sprintf("%s/api/v1/admin/system/db_snapshot", masterURL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	// Authentication
	if masterToken != "" {
		req.Header.Set("Authorization", "Bearer "+masterToken)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("master returned status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Create temp file
	out, err := os.Create(downloadPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write snapshot: %w", err)
	}
	out.Close() // Explicit close before attaching

	// Ensure cleanup
	defer os.Remove(downloadPath)

	// 2. ATTACH DATABASE and execute sync transaction
	// Warning: The path in ATTACH DATABASE must be an absolute or correct relative path.
	absPath, err := filepath.Abs(downloadPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of downloaded db: %w", err)
	}
	
	// Format absolute path for Windows SQLite properly (replace \ with /)
	absPath = filepath.ToSlash(absPath)


	// 3. Obtain a dedicated connection for ATTACH/DETACH
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get database connection: %w", err)
	}
	defer func() {
		// Force the connection to be destroyed instead of returning to the pool.
		// modernc.org/sqlite caches prepared statements implicitly, which counts as "active statements".
		// SQLite forbids DETACH when there are active statements, so DETACH always fails silently.
		// This poisons the connection. Destroying it is the only 100% safe workaround.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		conn.Close()
	}()

	// ATTACH must be done on the connection OUTSIDE the transaction
	_, err = conn.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS master_db", absPath))
	if err != nil {
		return fmt.Errorf("failed to attach master database: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "DETACH DATABASE master_db")
	}()

	// Execute sync transaction on the specific connection
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	queries := []string{
		"DELETE FROM main.channel_groups",
		"INSERT INTO main.channel_groups (id, name, icon, sort_order, is_direct, source, user_agent, custom_headers, enable_multiplex, proxy_type, proxy_url, created_at, updated_at) SELECT id, name, icon, sort_order, is_direct, source, user_agent, custom_headers, enable_multiplex, proxy_type, proxy_url, created_at, updated_at FROM master_db.channel_groups",
		
		"DELETE FROM main.channels",
		"INSERT INTO main.channels (id, group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_hidden, is_direct, sort_order, status, last_check, m3u_source_id, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days, enable_multiplex, proxy_type, proxy_url, content_type, fcc, fcc_type, linked_channel_id, is_protected, created_at, updated_at) SELECT id, group_id, name, logo, description, stream_url, stream_type, epg_channel_id, is_hidden, is_direct, sort_order, status, last_check, m3u_source_id, source, user_agent, custom_headers, support_catchup, catchup_type, catchup_source, catchup_days, enable_multiplex, proxy_type, proxy_url, COALESCE(content_type, ''), COALESCE(fcc, 0), COALESCE(fcc_type, ''), COALESCE(linked_channel_id, 0), COALESCE(is_protected, 0), created_at, updated_at FROM master_db.channels",
		
		"DELETE FROM main.subscription_plans",
		"INSERT INTO main.subscription_plans (id, name, days, max_streams, price, description, subscription_token, created_at, updated_at) SELECT id, name, days, max_streams, price, description, subscription_token, created_at, updated_at FROM master_db.subscription_plans",
		
		"DELETE FROM main.plan_group_relations",
		"INSERT INTO main.plan_group_relations (plan_id, group_id, sort_order) SELECT plan_id, group_id, sort_order FROM master_db.plan_group_relations",
		
		"DELETE FROM main.m3u_sources",
		"INSERT INTO main.m3u_sources (id, name, url, auto_sync, sync_interval, user_agent, custom_headers, proxy_type, proxy_url, last_sync, created_at) SELECT id, name, url, auto_sync, sync_interval, user_agent, custom_headers, proxy_type, proxy_url, last_sync, created_at FROM master_db.m3u_sources",

		`INSERT OR REPLACE INTO main.user_settings 
		 SELECT * FROM master_db.user_settings 
		 WHERE key NOT IN (
			'server_name',
			'server_url', 
			'admin_password_hash',
			'sync_enable', 
			'sync_master_url', 
			'sync_master_token', 
			'sync_serve_token',
			'sync_interval_min',
			'update_version_code', 
			'update_version_name', 
			'update_download_url', 
			'update_log', 
			'update_force'
		 )`,
	}

	for _, q := range queries {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("sync transaction failed on query: %s, error: %w", q, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit sync transaction: %w", err)
	}

	// 4. Sync logos incrementally
	if err := s.syncLogosFromMaster(masterURL, masterToken); err != nil {
		slog.Error("failed to sync logos from master", "error", err)
	}

	slog.Info("sync from master completed successfully")
	return nil
}

func (s *SyncService) syncLogosFromMaster(masterURL, masterToken string) error {
	apiURL := fmt.Sprintf("%s/api/v1/admin/system/logos_snapshot", masterURL)
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return err
	}
	if masterToken != "" {
		req.Header.Set("Authorization", "Bearer "+masterToken)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch logos snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("master returned status %d for logos snapshot", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Logos map[string]string `json:"logos"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to decode logos snapshot: %w", err)
	}

	masterLogos := result.Data.Logos
	if masterLogos == nil {
		masterLogos = make(map[string]string)
	}

	dir := "./library/channel_logo"
	os.MkdirAll(dir, 0755)

	entries, _ := os.ReadDir(dir)
	
	// Check existing files
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		
		name := entry.Name()
		filePath := filepath.Join(dir, name)
		
		masterHash, exists := masterLogos[name]
		if !exists {
			// Master deleted it, remove locally
			os.Remove(filePath)
			continue
		}
		
		// Check hash
		f, err := os.Open(filePath)
		if err == nil {
			h := md5.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash := fmt.Sprintf("%x", h.Sum(nil))
				if localHash == masterHash {
					// Same file, no need to download
					delete(masterLogos, name)
				}
			}
			f.Close()
		}
	}

	// Any remaining in masterLogos needs to be downloaded
	for name := range masterLogos {
		downloadURL := fmt.Sprintf("%s/library/channel_logo/%s", masterURL, name)
		slog.Info("Syncing logo from master", "file", name)
		
		// Simple GET download
		resp, err := client.Get(downloadURL)
		if err != nil {
			slog.Error("failed to download logo", "file", name, "error", err)
			continue
		}
		
		if resp.StatusCode == 200 {
			filePath := filepath.Join(dir, name)
			out, err := os.Create(filePath)
			if err == nil {
				if _, err := io.Copy(out, resp.Body); err != nil {
					slog.Error("failed to write downloaded logo", "file", name, "error", err)
				}
				out.Close()
			}
		}
		resp.Body.Close()
	}

	return nil
}
