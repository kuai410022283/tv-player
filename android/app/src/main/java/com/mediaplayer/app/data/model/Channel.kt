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
    @SerializedName("current_epg") var currentEpg: String = "",
    @SerializedName("next_epg") var nextEpg: String = "",
    @SerializedName("epg_percent") var epgPercent: Int = 0,

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

    fun getDynamicEpgPercent(): Int {
        if (currentEpg.isEmpty()) return epgPercent
        try {
            val parts = currentEpg.split(" ")
            if (parts.isNotEmpty()) {
                val timeRange = parts[0]
                val times = timeRange.split("-")
                if (times.size == 2) {
                    val sdf = java.text.SimpleDateFormat("HH:mm", java.util.Locale.getDefault())
                    val start = sdf.parse(times[0])
                    val end = sdf.parse(times[1])
                    
                    if (start != null && end != null) {
                        val now = java.util.Calendar.getInstance()
                        val startCal = java.util.Calendar.getInstance().apply { 
                            time = start
                            set(now.get(java.util.Calendar.YEAR), now.get(java.util.Calendar.MONTH), now.get(java.util.Calendar.DATE))
                        }
                        val endCal = java.util.Calendar.getInstance().apply {
                            time = end
                            set(now.get(java.util.Calendar.YEAR), now.get(java.util.Calendar.MONTH), now.get(java.util.Calendar.DATE))
                        }
                        
                        if (endCal.before(startCal)) {
                            if (now.get(java.util.Calendar.HOUR_OF_DAY) < 12) {
                                startCal.add(java.util.Calendar.DATE, -1)
                            } else {
                                endCal.add(java.util.Calendar.DATE, 1)
                            }
                        }
                        
                        val startTime = startCal.timeInMillis
                        val endTime = endCal.timeInMillis
                        val currentTime = now.timeInMillis
                        
                        if (currentTime in startTime..endTime) {
                            val duration = endTime - startTime
                            val elapsed = currentTime - startTime
                            return ((elapsed.toFloat() / duration) * 100).toInt().coerceIn(0, 100)
                        } else if (currentTime > endTime) {
                            return 100
                        } else {
                            return 0
                        }
                    }
                }
            }
        } catch (e: Exception) {
            // Ignore parse errors
        }
        return epgPercent
    }
}
