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

        // 启动本地 Go 代理（随机端口），用于直连组播流（udp:///rtp:///rtsp://）
        // 默认关闭，仅当用户在设置中开启后才启动，避免部分设备（如小米电视）Go runtime 兼容性问题导致闪退
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        if (prefs.getBoolean(Prefs.KEY_LOCAL_PROXY_ENABLED, false)) {
            val success = tryStartProxy()
            if (success) {
                isProxyEnabled = true
            } else {
                // 启动失败（如 MIUI TV 上 Go runtime 不兼容），自动回退关闭，避免下次启动再次尝试
                prefs.edit().putBoolean(Prefs.KEY_LOCAL_PROXY_ENABLED, false).apply()
                com.mediaplayer.app.util.RemoteLogger.e("MediaPlayerApp", "Go proxy failed on startup, auto-disabled")
            }
        }

        try {
            val clazz = Class.forName("androidx.media3.exoplayer.rtsp.RtspMessageLogger")
            val delegateClass = Class.forName("androidx.media3.exoplayer.rtsp.RtspMessageLogger\$LoggerDelegate")
            
            val proxy = java.lang.reflect.Proxy.newProxyInstance(
                delegateClass.classLoader,
                arrayOf(delegateClass)
            ) { _, method, args ->
                val tag = args[0] as String
                val msg = args[1] as String
                when (method.name) {
                    "d" -> com.mediaplayer.app.util.RemoteLogger.d(tag, msg)
                    "w" -> com.mediaplayer.app.util.RemoteLogger.e(tag, msg) // using e to save w
                    "e" -> {
                        val t = if (args.size > 2) args[2] as? Throwable else null
                        com.mediaplayer.app.util.RemoteLogger.e(tag, msg, t)
                    }
                }
                null
            }
            
            clazz.getMethod("setDelegate", delegateClass).invoke(null, proxy)
        } catch (e: Exception) {
            // Official Media3 without RtspMessageLogger, skip
            android.util.Log.i("MediaPlayerApp", "RTSP logger injection skipped.")
        }

        // Initialize API with saved server URL or default
        val serverUrl = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: Prefs.DEFAULT_SERVER_URL
        ApiClient.init(serverUrl)
        
        // Restore access token
        ApiClient.accessToken = prefs.getString(Prefs.KEY_ACCESS_TOKEN, null)

        // 异步预加载 AV3A 解码库（约 12MB），确保首次播放 AV3A 流时无需等待 ~3 秒的库加载延迟
        androidx.media3.decoder.av3a.Av3aLibrary.preloadAsync()
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
        /** 本地 Go 代理端口号，-1 表示不可用（一旦启动成功就不会重置，因为 Go 侧 proxyStarted 无法撤销） */
        @JvmStatic
        var localProxyPort: Int = -1
            internal set

        /** 用户是否开启了本地代理功能（控制 ExoPlayer 是否使用代理） */
        @JvmStatic
        var isProxyEnabled: Boolean = false
            internal set

        /**
         * 尝试启动本地 Go 代理，返回 true 表示启动成功或已启动。
         * 注意：必须用 Throwable 捕获，因为 System.loadLibrary 失败会抛出 UnsatisfiedLinkError（Error 子类）。
         * 部分设备（如小米电视）Go runtime 初始化可能触发原生层 SIGSEGV，该场景无法被捕获，
         * 因此此功能默认关闭，仅由用户在设置中手动开启。
         */
        fun tryStartProxy(): Boolean {
            if (localProxyPort > 0) {
                // 复用前先 TCP 探活，确认代理仍在监听（App 重启后 Go 运行时会重新初始化，旧端口可能已失效）
                val alive = try {
                    java.net.Socket().use { socket ->
                        socket.connect(java.net.InetSocketAddress("127.0.0.1", localProxyPort), 500)
                        true
                    }
                } catch (_: Exception) { false }
                if (alive) return true
                // 端口失效，清除旧端口，重新启动
                com.mediaplayer.app.util.RemoteLogger.e("MediaPlayerApp", "Go proxy port $localProxyPort no longer alive, restarting...")
                localProxyPort = -1
            }
            return try {
                val port = `mobile`.Mobile.startLocalProxy().toInt()
                if (port > 0) {
                    com.mediaplayer.app.util.RemoteLogger.i("MediaPlayerApp", "Go proxy started on port $port")
                    localProxyPort = port
                    true
                } else {
                    com.mediaplayer.app.util.RemoteLogger.i("MediaPlayerApp", "Go proxy returned invalid port: $port")
                    false
                }
            } catch (e: Throwable) {
                com.mediaplayer.app.util.RemoteLogger.e("MediaPlayerApp", "Go proxy failed to start: ${e.message}")
                false
            }
        }
    }
}
