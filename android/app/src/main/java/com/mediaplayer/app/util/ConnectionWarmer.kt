package com.mediaplayer.app.util

import com.mediaplayer.app.util.RemoteLogger

/**
 * 连接预热器：提前建立到服务器的TCP连接，减少首次请求的延迟。
 * 建立的连接会被 OkHttp 连接池复用，对光猫NAT场景效果明显。
 */
object ConnectionWarmer {

    private var warmed = false

    /**
     * 预热连接到指定服务器。
     * 会在后台线程执行，不阻塞主线程。
     * @param serverUrl 服务器地址，如 "http://nas.laokhome.cn:9527"
     */
    fun warmUp(serverUrl: String) {
        if (warmed) return
        warmed = true

        Thread {
            try {
                val url = java.net.URL(serverUrl)
                val connection = url.openConnection()
                connection.connectTimeout = 5000
                connection.connect()
                // 读取一小部分数据确保连接完全建立
                connection.getInputStream().close()
                RemoteLogger.i("ConnectionWarmer", "预连接成功: $serverUrl")
            } catch (e: Exception) {
                RemoteLogger.e("ConnectionWarmer", "预连接失败: ${e.message}")
            }
        }.start()
    }

    /**
     * 预热连接到多个服务器（用于备用服务器场景）。
     */
    fun warmUp(serverUrls: List<String>) {
        if (warmed) return
        warmed = true

        Thread {
            for (url in serverUrls) {
                try {
                    val connection = java.net.URL(url).openConnection()
                    connection.connectTimeout = 5000
                    connection.connect()
                    connection.getInputStream().close()
                    RemoteLogger.i("ConnectionWarmer", "预连接成功: $url")
                    break // 连接成功就够了，不需要预热所有服务器
                } catch (e: Exception) {
                    RemoteLogger.e("ConnectionWarmer", "预连接失败: $url - ${e.message}")
                }
            }
        }.start()
    }

    fun reset() {
        warmed = false
    }
}
