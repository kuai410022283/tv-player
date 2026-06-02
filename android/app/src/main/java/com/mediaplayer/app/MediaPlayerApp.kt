package com.mediaplayer.app

import android.app.Application
import com.mediaplayer.app.data.api.ApiClient

class MediaPlayerApp : Application() {

    override fun onCreate() {
        super.onCreate()
        instance = this

        // Initialize API with saved server URL or default
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val serverUrl = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: Prefs.DEFAULT_SERVER_URL
        ApiClient.init(serverUrl)
        
        // Restore access token
        ApiClient.accessToken = prefs.getString(Prefs.KEY_ACCESS_TOKEN, null)
    }

    companion object {
        lateinit var instance: MediaPlayerApp
            private set
    }
}
