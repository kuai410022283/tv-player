package com.tvplayer.app

import android.app.Application
import com.tvplayer.app.data.api.ApiClient

class TVPlayerApp : Application() {

    override fun onCreate() {
        super.onCreate()
        instance = this

        // Initialize API with saved server URL or default
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val serverUrl = prefs.getString(Prefs.KEY_SERVER_URL, "http://10.0.2.2:9527") ?: "http://10.0.2.2:9527"
        ApiClient.init(serverUrl)
    }

    companion object {
        lateinit var instance: TVPlayerApp
            private set
    }
}
