package com.tvplayer.app.data.model

import com.google.gson.annotations.SerializedName

data class Channel(
    @SerializedName("id") val id: Long = 0,
    @SerializedName("group_id") val groupId: Long = 0,
    @SerializedName("name") val name: String = "",
    @SerializedName("logo") val logo: String = "",
    @SerializedName("description") val description: String = "",
    @SerializedName("stream_url") val streamUrl: String = "",
    @SerializedName("stream_type") val streamType: String = "hls",
    @SerializedName("epg_channel_id") val epgChannelId: String = "",
    @SerializedName("is_favorite") val isFavorite: Boolean = false,
    @SerializedName("is_hidden") val isHidden: Boolean = false,
    @SerializedName("sort_order") val sortOrder: Int = 0,
    @SerializedName("status") val status: String = "unknown",
    @SerializedName("last_check") val lastCheck: String = "",
    @SerializedName("created_at") val createdAt: String = "",
    @SerializedName("updated_at") val updatedAt: String = ""
)
