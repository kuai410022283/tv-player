package com.mediaplayer.app.util

import com.mediaplayer.app.data.api.ApiClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.UUID

object PlaybackStateReporter {
    var currentSessionId: String? = null
    var currentChannelId: Long = 0

    suspend fun reportPlaying(channelId: Long, isProxy: Boolean, speedBytes: Long = 0, streamUrl: String = "") = withContext(Dispatchers.IO) {
        if (isProxy) {
            reportStopped()
            return@withContext
        }
        if (currentSessionId == null) {
            currentSessionId = UUID.randomUUID().toString()
        }
        currentChannelId = channelId

        try {
            val body = mutableMapOf<String, Any>(
                "channel_id" to channelId,
                "session_id" to currentSessionId!!,
                "status" to "playing",
                "speed_bytes" to speedBytes
            )
            if (streamUrl.isNotEmpty()) {
                body["url"] = streamUrl
            }
            ApiClient.getService().clientPlayingStatus(body)
        } catch (e: Exception) {
            // Ignore reporting errors
        }
    }

    suspend fun reportStopped() = withContext(Dispatchers.IO) {
        val sessionId = currentSessionId ?: return@withContext
        try {
            val body = mapOf<String, Any>(
                "channel_id" to currentChannelId,
                "session_id" to sessionId,
                "status" to "stopped"
            )
            ApiClient.getService().clientPlayingStatus(body)
        } catch (e: Exception) {
            // Ignore reporting errors
        } finally {
            currentSessionId = null
            currentChannelId = 0
        }
    }
}
