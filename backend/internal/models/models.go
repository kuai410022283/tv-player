package models

import (
	"sync"
	"time"
)

// ── Channel Group ──────────────────────────────────────

type ChannelGroup struct {
	ID              int64     `json:"id" db:"id"`
	Name            string    `json:"name" db:"name"`
	Icon            string    `json:"icon,omitempty" db:"icon"`
	SortOrder       int       `json:"sort_order" db:"sort_order"`
	IsDirect        bool      `json:"is_direct" db:"is_direct"`
	Source          string    `json:"source" db:"source"`
	ChannelCount    int       `json:"channel_count" db:"channel_count"`
	UserAgent       string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders   string    `json:"custom_headers,omitempty" db:"custom_headers"`
	EnableMultiplex int       `json:"enable_multiplex" db:"enable_multiplex"`
	CanMultiplex    bool      `json:"can_multiplex" db:"-"`
	NonMuxCount     int       `json:"non_mux_count" db:"-"`
	ProxyType       string    `json:"proxy_type,omitempty" db:"proxy_type"` // none, socks5, http, https
	ProxyURL        string    `json:"proxy_url,omitempty" db:"proxy_url"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
}

// ── Channel ────────────────────────────────────────────

type Channel struct {
	ID           int64  `json:"id" db:"id"`
	GroupID      int64  `json:"group_id" db:"group_id"`
	Name         string `json:"name" db:"name"`
	Logo         string `json:"logo,omitempty" db:"logo"`
	Description  string `json:"description,omitempty" db:"description"`
	StreamURL    string `json:"stream_url" db:"stream_url"`
	StreamType   string `json:"stream_type" db:"stream_type"` // hls, flv, rtmp, rtsp, mp4, dash
	EPGChannelID string `json:"epg_channel_id,omitempty" db:"epg_channel_id"`
	CurrentEPG   string `json:"current_epg,omitempty" db:"-"`
	NextEPG      string `json:"next_epg,omitempty" db:"-"`
	EpgPercent   int    `json:"epg_percent,omitempty" db:"-"`

	IsHidden        bool      `json:"is_hidden" db:"is_hidden"`
	IsEnabled       bool      `json:"is_enabled" db:"is_enabled"`
	IsDirect        bool      `json:"is_direct" db:"is_direct"`
	SortOrder       int       `json:"sort_order" db:"sort_order"`
	Status          string    `json:"status" db:"status"` // online, offline, unknown
	LastCheck       time.Time `json:"last_check,omitempty" db:"last_check"`
	M3USourceID     int64     `json:"m3u_source_id" db:"m3u_source_id"`
	Source          string    `json:"source" db:"source"`
	UserAgent       string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders   string    `json:"custom_headers,omitempty" db:"custom_headers"`
	SupportCatchup  bool      `json:"support_catchup" db:"support_catchup"`
	CatchupType     string    `json:"catchup_type,omitempty" db:"catchup_type"`
	CatchupSource   string    `json:"catchup_source,omitempty" db:"catchup_source"`
	CatchupDays     int       `json:"catchup_days,omitempty" db:"catchup_days"`
	EnableMultiplex int       `json:"enable_multiplex" db:"enable_multiplex"`
	ContentType     string    `json:"content_type" db:"content_type"` // live, vod, 空=自动推断
	Fcc             string    `json:"fcc,omitempty" db:"fcc"`
	FccType         string    `json:"fcc_type,omitempty" db:"fcc_type"`
	ProxyType       string    `json:"proxy_type,omitempty" db:"proxy_type"` // none, socks5, http, https
	ProxyURL        string    `json:"proxy_url,omitempty" db:"proxy_url"`
	CanMultiplex    bool      `json:"can_multiplex" db:"-"`
	LinkedChannelID int64     `json:"linked_channel_id" db:"linked_channel_id"`
	IsProtected     bool      `json:"is_protected" db:"is_protected"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
	UpdatedAt       time.Time `json:"updated_at" db:"updated_at"`
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
	ID                int64     `json:"id" db:"id"`
	Name              string    `json:"name" db:"name"`
	Days              int       `json:"days" db:"days"`               // 授权天数, 0表示永久
	MaxStreams        int       `json:"max_streams" db:"max_streams"` // 允许并发设备数
	Price             float64   `json:"price" db:"price"`             // 展示价格
	Description       string    `json:"description" db:"description"`
	SubscriptionToken string    `json:"subscription_token" db:"subscription_token"`
	EnableAggregation int       `json:"enable_aggregation" db:"enable_aggregation"`
	GroupIDs          []int64   `json:"group_ids" db:"-"` // 关联的频道分组（按顺序排列）
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}

type SubscriptionChannel struct {
	ID             int64  `json:"id"`
	GroupID        int64  `json:"group_id"`
	GroupName      string `json:"group_name"`
	Name           string `json:"name"`
	Logo           string `json:"logo"`
	StreamURL      string `json:"stream_url"`
	StreamType     string `json:"stream_type"`
	EPGChannelID   string `json:"epg_channel_id"`
	IsDirect       bool   `json:"is_direct"`
	SupportCatchup bool   `json:"support_catchup"`
	CatchupType    string `json:"catchup_type"`
	CatchupSource  string `json:"catchup_source"`
	CatchupDays    int    `json:"catchup_days"`
	UserAgent      string `json:"user_agent"`
	CustomHeaders  string `json:"custom_headers"`
	ContentType    string `json:"content_type"`
	Fcc            string `json:"fcc,omitempty"`
	FccType        string `json:"fcc_type,omitempty"`
	ProxyType      string `json:"proxy_type,omitempty"`
	ProxyURL       string `json:"proxy_url,omitempty"`
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
	ID            int64     `json:"id" db:"id"`
	Name          string    `json:"name" db:"name"`
	URL           string    `json:"url" db:"url"`
	AutoSync      bool      `json:"auto_sync" db:"auto_sync"`
	SyncInterval  int       `json:"sync_interval" db:"sync_interval"` // In hours
	UserAgent     string    `json:"user_agent,omitempty" db:"user_agent"`
	CustomHeaders string    `json:"custom_headers,omitempty" db:"custom_headers"`
	ProxyType     string    `json:"proxy_type,omitempty" db:"proxy_type"` // none, socks5, http, https
	ProxyURL      string    `json:"proxy_url,omitempty" db:"proxy_url"`
	LastSync      time.Time `json:"last_sync,omitempty" db:"last_sync"`
	SyncStatus    string    `json:"sync_status" db:"sync_status"` // idle, syncing, error
	SyncError     string    `json:"sync_error,omitempty" db:"sync_error"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// ── API Request / Response ─────────────────────────────

type PageRequest struct {
	Page     int `form:"page" json:"page"`
	PageSize int `form:"page_size" json:"page_size"`
}

func (p *PageRequest) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = 20
	}
	if p.PageSize > 5000 {
		p.PageSize = 5000
	} // 支持大分页，提升7000+频道场景加载速度
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
	ChannelID  int64  `json:"channel_id"`
	URL        string `json:"url"`
	Status     string `json:"status"` // playing, buffering, error, stopped
	Bitrate    int64  `json:"bitrate"`
	Resolution string `json:"resolution"`
	BufferPct  int    `json:"buffer_pct"`
	ErrorMsg   string `json:"error_msg,omitempty"`
}

// ── Client (设备授权) ─────────────────────────────────

type Client struct {
	ID           int64     `json:"id" db:"id"`
	Name         string    `json:"name" db:"name"`                           // 设备名称
	DeviceID     string    `json:"device_id" db:"device_id"`                 // 设备唯一标识
	DeviceModel  string    `json:"device_model" db:"device_model"`           // 设备型号
	DeviceOS     string    `json:"device_os" db:"device_os"`                 // 系统版本
	AppVersion   string    `json:"app_version" db:"app_version"`             // 客户端版本
	IP           string    `json:"ip" db:"ip"`                               // 最近连接IP
	AccessToken  string    `json:"access_token,omitempty" db:"access_token"` // 访问令牌
	TokenPreview string    `json:"token_preview,omitempty" db:"-"`           // 令牌预览
	Status       string    `json:"status" db:"status"`                       // pending, approved, rejected, banned, expired
	PlanID       int64     `json:"plan_id" db:"plan_id"`                     // 绑定的套餐ID
	PlanName     string    `json:"plan_name,omitempty" db:"-"`               // 套餐名称 (展示用)
	MaxStreams   int       `json:"max_streams" db:"max_streams"`             // 允许最大并发流数
	ExpiresAt    time.Time `json:"expires_at,omitempty" db:"expires_at"`     // 授权过期时间
	ApprovedBy   string    `json:"approved_by,omitempty" db:"approved_by"`   // 审批人
	RejectReason string    `json:"reject_reason,omitempty" db:"reject_reason"`
	LastSeen     time.Time `json:"last_seen,omitempty" db:"last_seen"`
	TotalPlayMin int64     `json:"total_play_minutes" db:"total_play_minutes"` // 累计播放分钟
	RequestNote  string    `json:"request_note,omitempty" db:"request_note"`   // 申请备注
	EnableLog    bool      `json:"enable_log" db:"enable_log"`                 // 是否采集日志
	IsTester     bool      `json:"is_tester" db:"is_tester"`                   // 是否为测试设备
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
	EnableLog   bool   `json:"enable_log"`

	// 维护与测试标识
	GlobalMaintenance bool `json:"global_maintenance"`
	IsTester          bool `json:"is_tester"`

	// 开机短视频/广告
	StartupMediaEnabled bool   `json:"startup_media_enabled"`
	StartupMedia        string `json:"startup_media"`
	StartupDuration     int    `json:"startup_duration"`
	StartupSkipAfter    int    `json:"startup_skip_after"`

	// 备用服务器分发 (Seed Node Distribution)
	BackupServers []string `json:"backup_servers,omitempty"`
}

// 客户端审批请求
type ClientApproveReq struct {
	PlanID     int64  `json:"plan_id"`     // 选定的套餐ID
	MaxDays    int    `json:"max_days"`    // 授权天数 (如果未选套餐, 可自定义)
	MaxStreams int    `json:"max_streams"` // 最大并发流 (如果未选套餐, 可自定义)
	Note       string `json:"note"`
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
	ActiveStreams  int   `json:"active_streams"`
	TotalClients   int   `json:"total_clients"`
	PendingClients int   `json:"pending_clients"`
	OnlineClients  int   `json:"online_clients"`
	Uptime         int64 `json:"uptime_seconds"`
	MemoryMB       int64 `json:"memory_mb"`
}

// ActiveStream 代表当前正在播放的活跃流状态
type ActiveStream struct {
	Mu          *sync.RWMutex `json:"-"`
	SessionID   string        `json:"session_id"`
	ClientID    int64         `json:"client_id"`
	ClientIP    string        `json:"client_ip"`
	ClientName  string        `json:"client_name"`
	ChannelID   int64         `json:"channel_id"`
	ChannelName string        `json:"channel_name"`
	URL         string        `json:"url"`
	Status      string        `json:"status"`
	SpeedBytes  int64         `json:"speed_bytes"` // 实时速度 (Bytes/s)
	IsDirect    bool          `json:"is_direct"`   // true=直连, false=代理
	ErrorMsg    string        `json:"error_msg,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	LastActive  time.Time     `json:"last_active"`
}

// PlayingStatusReq 代表客户端主动上报的播放状态
type PlayingStatusReq struct {
	ChannelID  int64  `json:"channel_id"`
	SessionID  string `json:"session_id"`            // 相同设备的多并发会话
	Status     string `json:"status"`                // playing, stopped
	SpeedBytes int64  `json:"speed_bytes,omitempty"` // 实时网速
	URL        string `json:"url,omitempty"`         // 客户端实际播放的URL
}

// ── Client Remote Config (客户端远程配置) ──────────────────

// ClientRemoteConfig 下发给客户端的远程配置，omitempty + 指针类型确保 null 值不序列化。
// 客户端收到某字段为 null/缺失时，保持本地设置不变（不管控语义）。
type ClientRemoteConfig struct {
	// 播放器行为
	PlayerCore        *int  `json:"player_core,omitempty"`         // 0=自动 1=ExoPlayer 3=MPV
	DecoderMode       *int  `json:"decoder_mode,omitempty"`        // 0=自动 1=硬解 2=软解
	NetworkCacheMs    *int  `json:"network_cache_ms,omitempty"`    // 网络缓冲毫秒
	AudioPassthrough  *bool `json:"audio_passthrough,omitempty"`          // 音频直通
	AudioNormalizer   *bool `json:"audio_normalizer_enabled,omitempty"` // 响度平滑防爆音
	ScaleMode         *int  `json:"scale_mode,omitempty"`          // 画面比例 0-5
	DnsPolicy         *int  `json:"dns_policy,omitempty"`          // DNS策略 0-2
	StopPreviousMedia *bool `json:"stop_previous_media,omitempty"` // 切台停止上一路

	// 界面显示
	ShowChannelLogo   *bool `json:"show_channel_logo,omitempty"`   // 显示台标
	ShowGroupSource   *bool `json:"show_group_source,omitempty"`   // 显示频道来源
	GlobalProgressBar *int  `json:"global_progress_bar,omitempty"` // 进度条 0=关 1=顶 2=底
	TimeShowMode      *int  `json:"time_show_mode,omitempty"`      // 时间显示 0=隐藏 1=常显 2=整点 3=半点
	ControlScheme     *int  `json:"control_scheme,omitempty"`      // 操控方案 0=现代 1=传统

	// 功能开关
	AutoStart          *bool `json:"auto_start,omitempty"`           // 开机自启
	EnablePip          *bool `json:"enable_pip,omitempty"`           // 画中画
	LocalProxyEnabled  *bool `json:"local_proxy_enabled,omitempty"`  // 本地代理
	GestureBrightness  *bool `json:"gesture_brightness,omitempty"`   // 手势亮度
	GestureVolume      *bool `json:"gesture_volume,omitempty"`       // 手势音量
	ReverseChannelKeys *bool `json:"reverse_channel_keys,omitempty"` // 反转频道键
	PreferredServerIndex *int `json:"preferred_server_index,omitempty"` // 服务器选择
	AutoCheckUpdate    *bool `json:"auto_check_update,omitempty"`    // 自动检查更新
	AppLanguage        *int  `json:"app_language,omitempty"`         // 多语言配置 (0=自动, 1=中文, 2=英文等)

	// 隐藏配置项（不在客户端UI中显示，但值仍生效）
	HiddenKeys []string `json:"hidden_keys,omitempty"`

	// 面板隐藏（禁用整个客户端面板）
	HideSettingsPanel *bool `json:"hide_settings_panel,omitempty"` // 隐藏设置栏
	HideChannelList   *bool `json:"hide_channel_list,omitempty"`   // 隐藏频道列表
	HideEpgPanel      *bool `json:"hide_epg_panel,omitempty"`      // 隐藏节目单(EPG)
	HideOsdPanel      *bool `json:"hide_osd_panel,omitempty"`      // 隐藏OSD信息面板
	HideCommunity     *bool `json:"hide_community,omitempty"`      // 隐藏交流互动
	HideQrConfig      *bool `json:"hide_qr_config,omitempty"`      // 隐藏扫码配置
}

// ClientConfigEntry 对应数据库 client_remote_configs 表的单条记录
type ClientConfigEntry struct {
	ID        int64  `json:"id" db:"id"`
	Scope     string `json:"scope" db:"scope"` // 'global' 或 'client:{id}'
	ConfigKey string `json:"config_key" db:"config_key"`
	ConfigVal string `json:"config_val" db:"config_val"` // 空字符串表示不管控（由前端传入 null 时转为空）
	Hidden    int    `json:"hidden" db:"hidden"`         // 是否在客户端UI中隐藏（0=显示 1=隐藏）
	UpdatedAt string `json:"updated_at" db:"updated_at"`
}

// ClientConfigSaveReq 批量保存配置请求（key→值，值为 null 时删除该管控项）
type ClientConfigSaveReq struct {
	Configs map[string]interface{} `json:"configs" binding:"required"`
	Hidden  map[string]bool        `json:"hidden,omitempty"` // key→是否在客户端UI中隐藏
}
