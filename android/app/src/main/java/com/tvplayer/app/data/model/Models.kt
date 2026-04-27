package com.tvplayer.app.data.model

import com.google.gson.annotations.SerializedName

// ── API Response Wrapper ───────────────────────────────

data class ApiResponse<T>(
    @SerializedName("code") val code: Int,
    @SerializedName("message") val message: String,
    @SerializedName("data") val data: T?
)

data class PageResponse<T>(
    @SerializedName("total") val total: Long,
    @SerializedName("page") val page: Int,
    @SerializedName("page_size") val pageSize: Int,
    @SerializedName("items") val items: List<T>
)

// ── Channel Group ──────────────────────────────────────

data class ChannelGroup(
    @SerializedName("id") val id: Long,
    @SerializedName("name") val name: String,
    @SerializedName("icon") val icon: String = "",
    @SerializedName("sort_order") val sortOrder: Int = 0
)

// ── Channel ────────────────────────────────────────────

data class Channel(
    @SerializedName("id") val id: Long,
    @SerializedName("group_id") val groupId: Long,
    @SerializedName("name") val name: String,
    @SerializedName("logo") val logo: String = "",
    @SerializedName("description") val description: String = "",
    @SerializedName("stream_url") val streamUrl: String,
    @SerializedName("stream_type") val streamType: String = "hls",
    @SerializedName("epg_channel_id") val epgChannelId: String = "",
    @SerializedName("is_favorite") val isFavorite: Boolean = false,
    @SerializedName("is_hidden") val isHidden: Boolean = false,
    @SerializedName("sort_order") val sortOrder: Int = 0,
    @SerializedName("status") val status: String = "unknown"
)

// ── Client Registration ────────────────────────────────

data class ClientRegisterReq(
    @SerializedName("name") val name: String,
    @SerializedName("device_id") val deviceId: String,
    @SerializedName("device_model") val deviceModel: String = "",
    @SerializedName("device_os") val deviceOs: String = "",
    @SerializedName("app_version") val appVersion: String = "",
    @SerializedName("note") val note: String = ""
)

data class ClientRegisterResp(
    @SerializedName("client_id") val clientId: Long,
    @SerializedName("status") val status: String,
    @SerializedName("access_token") val accessToken: String = "",
    @SerializedName("expires_at") val expiresAt: String = "",
    @SerializedName("message") val message: String = ""
)

// ── Stream Status ──────────────────────────────────────

data class StreamStatus(
    @SerializedName("channel_id") val channelId: Long,
    @SerializedName("url") val url: String,
    @SerializedName("status") val status: String,
    @SerializedName("bitrate") val bitrate: Long = 0,
    @SerializedName("resolution") val resolution: String = "",
    @SerializedName("buffer_pct") val bufferPct: Int = 0,
    @SerializedName("error_msg") val errorMsg: String = ""
)

// ── Play History ───────────────────────────────────────

data class PlayHistory(
    @SerializedName("id") val id: Long,
    @SerializedName("channel_id") val channelId: Long,
    @SerializedName("client_id") val clientId: Long = 0,
    @SerializedName("duration") val duration: Int = 0,
    @SerializedName("last_pos") val lastPos: Int = 0,
    @SerializedName("created_at") val createdAt: String = ""
)

// ── Server Stats ───────────────────────────────────────

data class ServerStats(
    @SerializedName("total_channels") val totalChannels: Int = 0,
    @SerializedName("online_channels") val onlineChannels: Int = 0,
    @SerializedName("active_streams") val activeStreams: Int = 0,
    @SerializedName("total_clients") val totalClients: Int = 0,
    @SerializedName("pending_clients") val pendingClients: Int = 0,
    @SerializedName("online_clients") val onlineClients: Int = 0,
    @SerializedName("uptime_seconds") val uptimeSeconds: Long = 0
)

// ── EPG Program ────────────────────────────────────────

data class EPGProgram(
    @SerializedName("id") val id: Long,
    @SerializedName("channel_id") val channelId: String,
    @SerializedName("title") val title: String,
    @SerializedName("start_time") val startTime: String,
    @SerializedName("end_time") val endTime: String,
    @SerializedName("description") val description: String = ""
)
