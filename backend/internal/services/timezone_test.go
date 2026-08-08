package services

import (
	"database/sql"
	"testing"
	"time"
)

// 创建一个包含 user_settings 表的内存数据库，用于测试
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	// 确保 user_settings 表存在
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS user_settings (
		key TEXT PRIMARY KEY NOT NULL,
		value TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		t.Fatalf("create table failed: %v", err)
	}
	return db
}

func setSetting(t *testing.T, db *sql.DB, key, value string) {
	t.Helper()
	_, err := db.Exec(`INSERT OR REPLACE INTO user_settings (key, value) VALUES (?, ?)`, key, value)
	if err != nil {
		t.Fatalf("setSetting failed: %v", err)
	}
}

// ── GetChinaTimezone 测试 ──

func TestGetChinaTimezone_AlwaysUTC8(t *testing.T) {
	loc := GetChinaTimezone()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	beijing := now.In(loc)

	if beijing.Hour() != 20 {
		t.Errorf("UTC 12:00 should be 20:00 in CST, got %02d:00", beijing.Hour())
	}

	_, offset := now.In(loc).Zone()
	if offset != 8*3600 {
		t.Errorf("expected offset 28800, got %d", offset)
	}
}

func TestGetChinaTimezone_IndependentOfEPGTimeShift(t *testing.T) {
	// GetChinaTimezone 不依赖 DB，始终返回 UTC+8
	loc := GetChinaTimezone()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	_, offset := now.Zone()
	if offset != 8*3600 {
		t.Errorf("expected UTC+8, got offset %d", offset)
	}
}

// ── GetEPGTimeShift 测试 ──

func TestGetEPGTimeShift_DefaultZero(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	svc := NewPlanService(db, nil)
	shift := svc.GetEPGTimeShift()
	if shift != 0 {
		t.Errorf("expected default 0, got %d", shift)
	}
}

func TestGetEPGTimeShift_Positive(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	setSetting(t, db, "epg_time_shift", "8")

	svc := NewPlanService(db, nil)
	shift := svc.GetEPGTimeShift()
	if shift != 8 {
		t.Errorf("expected 8, got %d", shift)
	}
}

func TestGetEPGTimeShift_Negative(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	setSetting(t, db, "epg_time_shift", "-5")

	svc := NewPlanService(db, nil)
	shift := svc.GetEPGTimeShift()
	if shift != -5 {
		t.Errorf("expected -5, got %d", shift)
	}
}

func TestGetEPGTimeShift_Invalid(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	setSetting(t, db, "epg_time_shift", "abc")

	svc := NewPlanService(db, nil)
	shift := svc.GetEPGTimeShift()
	if shift != 0 {
		t.Errorf("expected 0 for invalid value, got %d", shift)
	}
}

// ── 隔离性验证 ──

func TestTimezoneAndEPGTimeShift_Independent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// epg_time_shift = 0（默认），GetChinaTimezone 仍返回 UTC+8
	setSetting(t, db, "epg_time_shift", "0")

	svc := NewPlanService(db, nil)
	shift := svc.GetEPGTimeShift()
	if shift != 0 {
		t.Errorf("EPGTimeShift should be 0, got %d", shift)
	}

	loc := GetChinaTimezone()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, loc)
	_, offset := now.Zone()
	if offset != 8*3600 {
		t.Errorf("GetChinaTimezone should always be UTC+8, got offset %d", offset)
	}
}

// ── EPGService timezone 字段测试 ──

func TestEPGService_TimezoneField(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// 无论 epg_time_shift 设什么值，EPGService.timezone 始终 UTC+8
	setSetting(t, db, "epg_time_shift", "3")

	svc := NewEPGService(db)
	tz := svc.GetTimezone()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, tz)
	_, offset := now.Zone()
	if offset != 8*3600 {
		t.Errorf("EPGService.timezone should be UTC+8, got offset %d", offset)
	}
}

// ── 回看 URL 时间戳验证 ──

func TestTimezone_CatchupTimestamp(t *testing.T) {
	loc := GetChinaTimezone()

	// Unix 时间戳 1704067200 = 2024-01-01 00:00:00 UTC
	// 北京时间应为 2024-01-01 08:00:00
	ts := int64(1704067200)
	beijing := time.Unix(ts, 0).In(loc)

	if beijing.Year() != 2024 || beijing.Month() != 1 || beijing.Day() != 1 {
		t.Errorf("expected 2024-01-01, got %s", beijing.Format("2006-01-02"))
	}
	if beijing.Hour() != 8 || beijing.Minute() != 0 {
		t.Errorf("expected 08:00, got %02d:%02d", beijing.Hour(), beijing.Minute())
	}

	// 验证 format 输出符合回看 URL 格式要求
	formatted := beijing.Format("20060102150405")
	if formatted != "20240101080000" {
		t.Errorf("expected 20240101080000, got %s", formatted)
	}
}
