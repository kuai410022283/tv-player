package models

import "time"

// ── Channel Group ──────────────────────────────────────

type ChannelGroup struct {
	ID        int64     `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Icon      string    `json:"icon,omitempty" db:"icon"`
	SortOrder int       `json:"sort_order" db:"sort_order"`
	IsDirect  bool      `json:"is_direct" db:"is_direct"`
	Source       string    `json:"source" db:"source"`
	ChannelCount int       `json:"channel_count" db:"channel_count"`
	UserAgent    string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders string   `json:"custom_headers,omitempty" db:"custom_headers"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// ── Channel ────────────────────────────────────────────

type Channel struct {
	ID          int64     `json:"id" db:"id"`
	GroupID     int64     `json:"group_id" db:"group_id"`
	Name        string    `json:"name" db:"name"`
	Logo        string    `json:"logo,omitempty" db:"logo"`
	Description string    `json:"description,omitempty" db:"description"`
	StreamURL   string    `json:"stream_url" db:"stream_url"`
	StreamType  string    `json:"stream_type" db:"stream_type"` // hls, flv, rtmp, rtsp, mp4, dash
	EPGChannelID string   `json:"epg_channel_id,omitempty" db:"epg_channel_id"`
	CurrentEPG   string   `json:"current_epg,omitempty" db:"-"`
	EpgPercent   int      `json:"epg_percent,omitempty" db:"-"`

	IsHidden    bool      `json:"is_hidden" db:"is_hidden"`
	IsDirect    bool      `json:"is_direct" db:"is_direct"`
	SortOrder   int       `json:"sort_order" db:"sort_order"`
	Status      string    `json:"status" db:"status"` // online, offline, unknown
	LastCheck   time.Time `json:"last_check,omitempty" db:"last_check"`
	M3USourceID   int64     `json:"m3u_source_id" db:"m3u_source_id"`
	Source        string    `json:"source" db:"source"`
	UserAgent     string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders string    `json:"custom_headers,omitempty" db:"custom_headers"`
	SupportCatchup bool     `json:"support_catchup" db:"support_catchup"`
	CatchupType    string   `json:"catchup_type,omitempty" db:"catchup_type"`
	CatchupSource  string   `json:"catchup_source,omitempty" db:"catchup_source"`
	CatchupDays    int      `json:"catchup_days,omitempty" db:"catchup_days"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// ── EPG (Electronic Program Guide) ─────────────────────

type EPGProgram struct {
	ID        int64     `json:"id" db:"id"`
	ChannelID string    `json:"channel_id" db:"epg_channel_id"`
	Title     string    `json:"title" db:"title"`
	StartTime time.Time `json:"start_time" db:"start_time"`
	EndTime   time.Time `json:"end_time" db:"end_time"`
	Desc      string    `json:"description,omitempty" db:"description"`
	CanReplay bool      `json:"can_replay,omitempty" db:"-"`
	ReplayURL string    `json:"replay_url,omitempty" db:"-"`
}

// ── Playback History ───────────────────────────────────

type PlayHistory struct {
	ID        int64     `json:"id" db:"id"`
	ChannelID int64     `json:"channel_id" db:"channel_id"`
	ClientID  int64     `json:"client_id,omitempty" db:"client_id"`
	Duration  int       `json:"duration" db:"duration"` // seconds watched
	LastPos   int       `json:"last_pos" db:"last_pos"` // last position in seconds
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// ── Subscription Plan (套餐) ──────────────────────────

type SubscriptionPlan struct {
	ID          int64     `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Days        int       `json:"days" db:"days"`               // 授权天数, 0表示永久
	MaxStreams  int       `json:"max_streams" db:"max_streams"` // 允许并发设备数
	Price       float64   `json:"price" db:"price"`             // 展示价格
	Description string    `json:"description" db:"description"`
	GroupIDs    []int64   `json:"group_ids" db:"-"`             // 关联的频道分组
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// ── User Settings ──────────────────────────────────────

type UserSetting struct {
	Key   string `json:"key" db:"key"`
	Value string `json:"value" db:"value"`
}

// ── App Update ─────────────────────────────────────────

type AppUpdateConfig struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	DownloadURL string `json:"download_url"`
	UpdateLog   string `json:"update_log"`
	ForceUpdate bool   `json:"force_update"`
}

// ── Source M3U ─────────────────────────────────────────

type M3USource struct {
	ID        int64     `json:"id" db:"id"`
	Name         string    `json:"name" db:"name"`
	URL          string    `json:"url" db:"url"`
	AutoSync     bool      `json:"auto_sync" db:"auto_sync"`
	SyncInterval int       `json:"sync_interval" db:"sync_interval"` // In hours
	UserAgent    string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders string   `json:"custom_headers,omitempty" db:"custom_headers"`
	LastSync     time.Time `json:"last_sync,omitempty" db:"last_sync"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}

// ── API Request / Response ─────────────────────────────

type PageRequest struct {
	Page     int `form:"page" json:"page"`
	PageSize int `form:"page_size" json:"page_size"`
}

func (p *PageRequest) Normalize() {
	if p.Page < 1 { p.Page = 1 }
	if p.PageSize < 1 { p.PageSize = 20 }
	if p.PageSize > 200 { p.PageSize = 200 }
}

type PageResponse struct {
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
	Items    interface{} `json:"items"`
}

type APIResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type StreamStatus struct {
	ChannelID   int64  `json:"channel_id"`
	URL         string `json:"url"`
	Status      string `json:"status"` // playing, buffering, error, stopped
	Bitrate     int64  `json:"bitrate"`
	Resolution  string `json:"resolution"`
	BufferPct   int    `json:"buffer_pct"`
	ErrorMsg    string `json:"error_msg,omitempty"`
}

// ── Client (设备授权) ─────────────────────────────────

type Client struct {
	ID           int64     `json:"id" db:"id"`
	Name         string    `json:"name" db:"name"`                   // 设备名称
	DeviceID     string    `json:"device_id" db:"device_id"`         // 设备唯一标识
	DeviceModel  string    `json:"device_model" db:"device_model"`   // 设备型号
	DeviceOS     string    `json:"device_os" db:"device_os"`         // 系统版本
	AppVersion   string    `json:"app_version" db:"app_version"`     // 客户端版本
	IP           string    `json:"ip" db:"ip"`                       // 最近连接IP
	AccessToken  string    `json:"-" db:"access_token"`              // 访问令牌 (对外不暴露)
	TokenPreview string    `json:"token_preview,omitempty" db:"-"`   // 令牌预览 (前8位...)
	Status       string    `json:"status" db:"status"`               // pending, approved, rejected, banned, expired
	PlanID       int64     `json:"plan_id" db:"plan_id"`             // 绑定的套餐ID
	PlanName     string    `json:"plan_name,omitempty" db:"-"`       // 套餐名称 (展示用)
	MaxStreams    int       `json:"max_streams" db:"max_streams"`     // 允许最大并发流数
	ExpiresAt    time.Time `json:"expires_at,omitempty" db:"expires_at"` // 授权过期时间
	ApprovedBy   string    `json:"approved_by,omitempty" db:"approved_by"` // 审批人
	RejectReason string    `json:"reject_reason,omitempty" db:"reject_reason"`
	LastSeen     time.Time `json:"last_seen,omitempty" db:"last_seen"`
	TotalPlayMin int64     `json:"total_play_minutes" db:"total_play_minutes"` // 累计播放分钟
	RequestNote  string    `json:"request_note,omitempty" db:"request_note"` // 申请备注
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// 客户端注册请求
type ClientRegisterReq struct {
	Name        string `json:"name" binding:"required"`
	DeviceID    string `json:"device_id" binding:"required"`
	DeviceModel string `json:"device_model"`
	DeviceOS    string `json:"device_os"`
	AppVersion  string `json:"app_version"`
	Note        string `json:"note"`
}

// 客户端注册响应
type ClientRegisterResp struct {
	ClientID    int64  `json:"client_id"`
	Status      string `json:"status"`
	AccessToken string `json:"access_token,omitempty"` // 仅 approved 时返回
	ExpiresAt   string `json:"expires_at,omitempty"`
	Message     string `json:"message"`
}

// 客户端审批请求
type ClientApproveReq struct {
	PlanID    int64  `json:"plan_id"`    // 选定的套餐ID
	MaxDays   int    `json:"max_days"`   // 授权天数 (如果未选套餐, 可自定义)
	MaxStreams int   `json:"max_streams"` // 最大并发流 (如果未选套餐, 可自定义)
	Note      string `json:"note"`
}

// 客户端拒绝请求
type ClientRejectReq struct {
	Reason string `json:"reason" binding:"required"`
}

// 客户端批量操作
type ClientBatchReq struct {
	IDs    []int64 `json:"ids" binding:"required"`
	Action string  `json:"action" binding:"required"` // approve, reject, ban, delete
}

// ── License (可选：许可证模式) ─────────────────────────

type License struct {
	ID          int64     `json:"id" db:"id"`
	Key         string    `json:"key" db:"license_key"`
	ClientID    int64     `json:"client_id" db:"client_id"`
	MaxDevices  int       `json:"max_devices" db:"max_devices"`
	MaxStreams  int       `json:"max_streams" db:"max_streams"`
	Features    string    `json:"features" db:"features"` // JSON: ["hd","4k","dvr"]
	ExpiresAt   time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// ── Access Log ─────────────────────────────────────────

type AccessLog struct {
	ID          int64     `json:"id" db:"id"`
	ClientID    int64     `json:"client_id" db:"client_id"`
	ClientName  string    `json:"client_name" db:"client_name"`
	Action      string    `json:"action" db:"action"` // login, play, heartbeat, error
	ChannelID   int64     `json:"channel_id,omitempty" db:"channel_id"`
	ChannelName string    `json:"channel_name,omitempty" db:"channel_name"`
	IP          string    `json:"ip" db:"ip"`
	UserAgent   string    `json:"user_agent,omitempty" db:"user_agent"`
	Detail      string    `json:"detail,omitempty" db:"detail"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// ── Server Stats ───────────────────────────────────────

type ServerStats struct {
	TotalChannels  int   `json:"total_channels"`
	OnlineChannels int   `json:"online_channels"`
	ActiveStreams   int   `json:"active_streams"`
	TotalClients   int   `json:"total_clients"`
	PendingClients int   `json:"pending_clients"`
	OnlineClients  int   `json:"online_clients"`
	Uptime         int64 `json:"uptime_seconds"`
	MemoryMB       int64 `json:"memory_mb"`
}

// ActiveStream 代表当前正在通过服务端代理转发的活跃流状态
type ActiveStream struct {
	SessionID   string    `json:"session_id"`
	ClientID    int64     `json:"client_id"`
	ClientIP    string    `json:"client_ip"`
	ClientName  string    `json:"client_name"`
	ChannelID   int64     `json:"channel_id"`
	ChannelName string    `json:"channel_name"`
	URL         string    `json:"url"`
	Status      string    `json:"status"`
	SpeedBytes  int64     `json:"speed_bytes"` // 实时速度 (Bytes/s)
	ErrorMsg    string    `json:"error_msg,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	LastActive  time.Time `json:"last_active"`
}
