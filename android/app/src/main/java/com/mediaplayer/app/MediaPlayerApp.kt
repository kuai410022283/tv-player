package com.mediaplayer.app

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import com.mediaplayer.app.data.api.ApiClient

class MediaPlayerApp : Application(), ImageLoaderFactory {

    override fun onCreate() {
        super.onCreate()
        instance = this

        com.mediaplayer.app.util.CrashHandler.instance.init(this)
        com.mediaplayer.app.util.RemoteLogger.init(this)

        // Initialize API with saved server URL or default
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val serverUrl = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: Prefs.DEFAULT_SERVER_URL
        ApiClient.init(serverUrl)
        
        // Restore access token
        ApiClient.accessToken = prefs.getString(Prefs.KEY_ACCESS_TOKEN, null)
    }

    override fun newImageLoader(): ImageLoader {
        return ImageLoader.Builder(this)
            .okHttpClient {
                ApiClient.getOkHttpClient()
            }
            .build()
    }

    companion object {
        lateinit var instance: MediaPlayerApp
            private set
    }
}
