package com.mediaplayer.app.data.model

import com.google.gson.annotations.SerializedName

data class ChannelLine(
    @SerializedName("id") val id: Long = 0,
    @SerializedName("stream_url") val streamUrl: String = "",
    @SerializedName("stream_type") val streamType: String = "hls",
    @SerializedName("user_agent") val userAgent: String = "",
    @SerializedName("custom_headers") val customHeaders: String = "",
    @SerializedName("support_catchup") val supportCatchup: Boolean = false,
    @SerializedName("catchup_days") val catchupDays: Int = 0
)

data class Channel(
    @SerializedName("id") val id: Long = 0,
    @SerializedName("group_id") val groupId: Long = 0,
    @SerializedName("name") val name: String = "",
    @SerializedName("logo") val logo: String = "",
    @SerializedName("description") val description: String = "",
    @SerializedName("current_epg") val currentEpg: String = "",
    @SerializedName("next_epg") val nextEpg: String = "",
    @SerializedName("epg_percent") val epgPercent: Int = 0,

    @SerializedName("sort_order") val sortOrder: Int = 0,
    @SerializedName("lines") val lines: List<ChannelLine> = emptyList(),
    
    // 兼容旧接口的冗余字段（避免崩溃）
    @SerializedName("stream_url") val legacyStreamUrl: String = "",
    @SerializedName("stream_type") val legacyStreamType: String = "hls",
    @SerializedName("user_agent") val legacyUserAgent: String = "",
    @SerializedName("custom_headers") val legacyCustomHeaders: String = "",
    @SerializedName("support_catchup") val supportCatchup: Boolean = false,
    @SerializedName("catchup_type") val catchupType: String = "",
    @SerializedName("catchup_source") val catchupSource: String = "",
    @SerializedName("catchup_days") val catchupDays: Int = 0
) {
    @Transient var globalIndex: Int = -1

    fun getLinesSafely(): List<ChannelLine> {
        if (lines.isNotEmpty()) return lines
        if (legacyStreamUrl.isNotEmpty()) {
            return listOf(ChannelLine(id, legacyStreamUrl, legacyStreamType, legacyUserAgent, legacyCustomHeaders, supportCatchup, catchupDays))
        }
        return emptyList()
    }
}
