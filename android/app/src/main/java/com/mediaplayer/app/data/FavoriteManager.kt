package com.mediaplayer.app.data

import android.content.Context
import android.content.SharedPreferences

/**
 * 频道收藏管理器
 */
object FavoriteManager {
    private const val PREFS_NAME = "favorite_channels_prefs"
    private const val KEY_FAVORITES = "favorite_ids"
    
    private lateinit var prefs: SharedPreferences
    private val favoriteIds = mutableSetOf<Long>()

    fun init(context: Context) {
        prefs = context.getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val saved = prefs.getStringSet(KEY_FAVORITES, emptySet()) ?: emptySet()
        favoriteIds.clear()
        favoriteIds.addAll(saved.mapNotNull { it.toLongOrNull() })
    }

    fun addFavorite(channelId: Long) {
        favoriteIds.add(channelId)
        save()
    }

    fun removeFavorite(channelId: Long) {
        favoriteIds.remove(channelId)
        save()
    }

    fun toggleFavorite(channelId: Long): Boolean {
        return if (favoriteIds.contains(channelId)) {
            removeFavorite(channelId)
            false
        } else {
            addFavorite(channelId)
            true
        }
    }

    fun isFavorite(channelId: Long): Boolean {
        return favoriteIds.contains(channelId)
    }

    fun getFavorites(): Set<Long> {
        return favoriteIds.toSet()
    }

    private fun save() {
        prefs.edit().putStringSet(KEY_FAVORITES, favoriteIds.map { it.toString() }.toSet()).apply()
    }
}
