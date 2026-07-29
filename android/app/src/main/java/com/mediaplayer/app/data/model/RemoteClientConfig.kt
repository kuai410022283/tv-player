package com.mediaplayer.app.data.model

import com.google.gson.annotations.SerializedName

/**
 * 后端下发的客户端远程配置。
 * 字段为 null 表示"不管控"——客户端保持本地设置不变。
 * 非 null 值将覆盖客户端本地设置。
 */
data class RemoteClientConfig(
    // ── 播放器行为 ──────────────────────────────────
    /** 播放内核: 0=自动 1=ExoPlayer 3=MPV */
    @SerializedName("player_core")         val playerCore: Int? = null,
    /** 解码模式: 0=自动 1=强制硬解 2=软解 */
    @SerializedName("decoder_mode")        val decoderMode: Int? = null,
    /** 网络缓冲毫秒，0=智能 */
    @SerializedName("network_cache_ms")    val networkCacheMs: Int? = null,
    /** 音频直通 */
    @SerializedName("audio_passthrough")   val audioPassthrough: Boolean? = null,
    /** 画面比例: 0-5 */
    @SerializedName("scale_mode")          val scaleMode: Int? = null,
    /** DNS策略: 0=自动 1=仅IPv4 2=仅IPv6 */
    @SerializedName("dns_policy")          val dnsPolicy: Int? = null,
    /** 切台时停止上一路媒体 */
    @SerializedName("stop_previous_media") val stopPreviousMedia: Boolean? = null,

    // ── 界面显示 ──────────────────────────────────
    /** 显示频道台标 */
    @SerializedName("show_channel_logo")   val showChannelLogo: Boolean? = null,
    /** 显示频道来源 */
    @SerializedName("show_group_source")   val showGroupSource: Boolean? = null,
    /** 进度条: 0=关 1=顶部 2=底部 */
    @SerializedName("global_progress_bar") val globalProgressBar: Int? = null,
    /** 时间显示: 0=隐藏 1=常显 2=整点 3=半点 */
    @SerializedName("time_show_mode")      val timeShowMode: Int? = null,
    /** 操控方案: 0=现代 1=传统 */
    @SerializedName("control_scheme")      val controlScheme: Int? = null,

    // ── 功能开关 ──────────────────────────────────
    /** 开机自启 */
    @SerializedName("auto_start")          val autoStart: Boolean? = null,
    /** 画中画 */
    @SerializedName("enable_pip")          val enablePip: Boolean? = null,
    /** 本地组播代理 */
    @SerializedName("local_proxy_enabled") val localProxyEnabled: Boolean? = null,
    /** 手势调节亮度 */
    @SerializedName("gesture_brightness")  val gestureBrightness: Boolean? = null,
    /** 手势调节音量 */
    @SerializedName("gesture_volume")      val gestureVolume: Boolean? = null,
    /** 反转频道切换键方向 */
    @SerializedName("reverse_channel_keys") val reverseChannelKeys: Boolean? = null,
    /** 自动检测更新 */
    @SerializedName("auto_check_update")   val autoCheckUpdate: Boolean? = null,
    /** 优选服务器索引 */
    @SerializedName("preferred_server_index") val preferredServerIndex: Int? = null,
    /** 界面多语言设置 */
    @SerializedName("app_language")        val appLanguage: Int? = null,

    // ── 隐藏配置项（不在客户端UI中显示，但值仍生效） ──
    /** 需要隐藏的配置项键名列表 */
    @SerializedName("hidden_keys")           val hiddenKeys: List<String>? = null,

    // ── 面板隐藏（禁用整个客户端面板） ──
    /** 隐藏设置栏 */
    @SerializedName("hide_settings_panel")   val hideSettingsPanel: Boolean? = null,
    /** 隐藏频道列表 */
    @SerializedName("hide_channel_list")     val hideChannelList: Boolean? = null,
    /** 隐藏节目单(EPG) */
    @SerializedName("hide_epg_panel")        val hideEpgPanel: Boolean? = null,
    /** 隐藏OSD信息面板 */
    @SerializedName("hide_osd_panel")        val hideOsdPanel: Boolean? = null
)
