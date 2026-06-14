package com.mediaplayer.app.util

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.withContext
import kotlinx.coroutines.suspendCancellableCoroutine
import okhttp3.Call
import okhttp3.Callback
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.TimeUnit
import kotlin.coroutines.resume
import kotlin.coroutines.resumeWithException

object StreamResolver {

    // 使用不自动跟随重定向的客户端，手动接管重定向逻辑，支持 HTTPS <-> HTTP 的跨协议降级/升级
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .build()

    private suspend fun executeCallAsync(call: Call): Response = suspendCancellableCoroutine { continuation ->
        continuation.invokeOnCancellation {
            call.cancel()
        }
        call.enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                if (continuation.isActive) {
                    continuation.resumeWithException(e)
                }
            }

            override fun onResponse(call: Call, response: Response) {
                if (continuation.isActive) {
                    continuation.resume(response)
                } else {
                    response.close()
                }
            }
        })
    }

    suspend fun resolve(originalUrl: String, userAgent: String?, customHeaders: String?): String {
        return withContext(Dispatchers.IO) {
            var currentUrl = originalUrl
            
            // 组播和特殊协议无需解析重定向，直接返回，大幅提升起播速度
            val lowerUrl = currentUrl.lowercase()
            if (lowerUrl.startsWith("udp://") || lowerUrl.startsWith("rtp://") || lowerUrl.startsWith("p3p://")) {
                return@withContext currentUrl
            }
            
            if (currentUrl.startsWith("/")) {
                val serverUrl = com.mediaplayer.app.data.api.ApiClient.getServerUrl().trimEnd('/')
                currentUrl = serverUrl + currentUrl
            }
            var redirects = 0
            val maxRedirects = 5

            while (redirects < maxRedirects) {
                // 检查协程是否已被取消，及时退出避免不必要的 HTTP 请求
                if (!isActive) return@withContext currentUrl

                try {
                    val requestBuilder = Request.Builder().url(currentUrl)

                    val ua = if (userAgent.isNullOrEmpty()) "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player" else userAgent
                    requestBuilder.header("User-Agent", ua)

                    if (!customHeaders.isNullOrEmpty()) {
                        try {
                            val json = JSONObject(customHeaders)
                            val keys = json.keys()
                            while (keys.hasNext()) {
                                val key = keys.next()
                                val value = json.getString(key)
                                requestBuilder.header(key, value)
                            }
                        } catch (e: Exception) {
                            e.printStackTrace()
                        }
                    }

                    // 使用 GET 请求，因为有些服务端会拦截 HEAD 请求。
                    // 只要在读取到响应头后立即调用 response.close()，就不会下载响应体，不会浪费带宽。
                    requestBuilder.get()

                    val call = client.newCall(requestBuilder.build())
                    
                    // 异步挂起执行，如果此时用户切台，协程被取消，底层的 call.cancel() 会立即掐断 TCP 连接
                    // 极大地释放了服务端的并发连接数限制，避免了起播卡顿
                    val response = executeCallAsync(call)
                    
                    val code = response.code
                    val isRedirect = code in 300..399

                    if (isRedirect) {
                        val location = response.header("Location")
                        response.close()
                        if (!location.isNullOrEmpty()) {
                            currentUrl = if (location.startsWith("http://", ignoreCase = true) || location.startsWith("https://", ignoreCase = true)) {
                                location
                            } else {
                                val baseUri = java.net.URI(currentUrl)
                                baseUri.resolve(location).toString()
                            }
                            redirects++
                            continue
                        } else {
                            break
                        }
                    } else {
                        // 遇到非重定向状态（如 200 OK），说明这就是真实的流地址
                        response.close()
                        break
                    }
                } catch (e: Exception) {
                    e.printStackTrace()
                    break // 发生异常时直接中断，返回目前获取到的地址
                }
            }
            currentUrl
        }
    }
}
