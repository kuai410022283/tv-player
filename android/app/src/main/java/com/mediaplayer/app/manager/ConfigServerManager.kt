package com.mediaplayer.app.manager

import android.content.Context
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.data.api.ApiClient
import com.mediaplayer.app.data.api.ClientAuthManager
import com.mediaplayer.app.server.ConfigWebServer

class ConfigServerManager(
    private val context: Context,
    private val authManager: ClientAuthManager,
    private val onUrlUpdated: () -> Unit
) {
    private var configWebServer: ConfigWebServer? = null

    fun startServer() {
        if (configWebServer == null) {
            configWebServer = ConfigWebServer(context, 9528) { rawUrl ->
                val newUrl = ApiClient.formatUrl(rawUrl)
                val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
                prefs.edit().putString(Prefs.KEY_SERVER_URL, newUrl).apply()
                ApiClient.init(newUrl)
                authManager.clearAuth()
                onUrlUpdated()
            }
            try {
                configWebServer?.start()
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
    }

    fun stopServer() {
        configWebServer?.stop()
        configWebServer = null
    }
}
