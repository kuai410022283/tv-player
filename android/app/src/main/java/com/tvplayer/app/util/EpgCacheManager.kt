package com.tvplayer.app.util

import com.tvplayer.app.data.model.EPGProgram

object EpgCacheManager {
    private val cache = mutableMapOf<String, CacheEntry>()
    private const val CACHE_DURATION_MS = 5 * 60 * 1000 // 5 minutes

    private class CacheEntry(
        val timestamp: Long,
        val programs: List<EPGProgram>
    )

    fun get(channelName: String): List<EPGProgram>? {
        val entry = cache[channelName] ?: return null
        if (System.currentTimeMillis() - entry.timestamp > CACHE_DURATION_MS) {
            cache.remove(channelName)
            return null
        }
        return entry.programs
    }

    fun put(channelName: String, programs: List<EPGProgram>) {
        cache[channelName] = CacheEntry(System.currentTimeMillis(), programs)
    }

    fun clear() {
        cache.clear()
    }
}
