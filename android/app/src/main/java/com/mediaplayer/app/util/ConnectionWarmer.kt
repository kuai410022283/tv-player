package com.mediaplayer.app.util

import com.mediaplayer.app.util.RemoteLogger
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/**
 * 连接预热器：提前建立到服务器的 TCP 连接，减少首次请求延迟。
 * 使用 OkHttp 连接池，预热建立的连接可被后续 Retrofit 请求复用。
 * 对反向代理 / 内网穿透场景尤其重要，可提前完成 TCP + TLS 握手。
 */
object ConnectionWarmer {

    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .followRedirects(true)
        .build()

    /**
     * 预热连接到指定服务器。
     * 使用 OkHttp 执行轻量 HEAD 请求，连接的 TCP/TLS 握手结果会被 OkHttp 连接池复用。
     */
    fun warmUp(serverUrl: String) {
        if (serverUrl.isBlank()) return

        Thread {
            try {
                val url = serverUrl.trimEnd('/')
                // 发一个轻量 HEAD 请求到根路径，提前完成 TCP/TLS 握手
                val request = Request.Builder()
                    .url("$url/")
                    .head()
                    .build()
                client.newCall(request).execute().use { response ->
                    // 只要连接建立成功就算预热完成，不关心具体响应码
                    RemoteLogger.i("ConnectionWarmer", "预连接成功: $serverUrl (${response.code})")
                }
            } catch (e: Exception) {
                // 预热失败不阻塞，仅记录日志
                RemoteLogger.i("ConnectionWarmer", "预连接失败: $serverUrl - ${e.message}")
            }
        }.start()
    }

    /**
     * 预热连接到多个服务器（并行预热）。
     * 所有服务器同时预热，提高整体启动速度。
     */
    fun warmUp(serverUrls: List<String>) {
        for (url in serverUrls) {
            if (url.isNotBlank()) {
                warmUp(url)
            }
        }
    }
}