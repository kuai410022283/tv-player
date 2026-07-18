package com.mediaplayer.app.data.api

import com.google.gson.GsonBuilder
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.logging.HttpLoggingInterceptor
import org.json.JSONObject
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory
import java.net.Inet4Address
import java.net.InetAddress
import java.util.concurrent.TimeUnit

/**
 * API 客户端单例 —— 管理 Retrofit 实例和服务器地址。
 * 使用前必须调用 [init] 设置服务器地址。
 */
object ApiClient {

    private var serverUrl: String = com.mediaplayer.app.Prefs.DEFAULT_SERVER_URL
    private var retrofit: Retrofit? = null
    private var apiService: ApiService? = null
    private var okHttpClient: OkHttpClient? = null

    /** 当前使用的 token（由 ClientAuthManager 设置） */
    var accessToken: String? = null

    fun formatUrl(url: String): String {
        var clean = url.trim().trimEnd('/')
        if (clean.isNotEmpty() && !clean.startsWith("http://", ignoreCase = true) && !clean.startsWith("https://", ignoreCase = true)) {
            clean = "http://$clean"
        }
        return clean
    }

    fun init(url: String) {
        val normalized = formatUrl(url)
        if (normalized == serverUrl && retrofit != null) return
        serverUrl = normalized
        retrofit = null
        apiService = null
        okHttpClient = null
    }

    fun reset() {
        retrofit = null
        apiService = null
        okHttpClient = null
        accessToken = null
    }

    fun getServerUrl(): String = serverUrl

    fun getService(): ApiService {
        if (apiService == null) {
            apiService = getRetrofit().create(ApiService::class.java)
        }
        return apiService!!
    }

    fun getOkHttpClient(): OkHttpClient {
        if (okHttpClient == null) {
            val logging = HttpLoggingInterceptor(object : HttpLoggingInterceptor.Logger {
                override fun log(message: String) {
                    if (message.contains("Exception") || message.contains("Failed") || message.contains("error", ignoreCase = true)) {
                        com.mediaplayer.app.util.RemoteLogger.e("OkHttp", message)
                    } else {
                        com.mediaplayer.app.util.RemoteLogger.d("OkHttp", message)
                    }
                }
            }).apply {
                level = HttpLoggingInterceptor.Level.BASIC
            }

            okHttpClient = OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .writeTimeout(15, TimeUnit.SECONDS)
                .addInterceptor { chain ->
                    val original = chain.request()
                    val builder = original.newBuilder()
                    val requestUrl = original.url.toString()
                    if (requestUrl.startsWith(serverUrl)) {
                        accessToken?.let { builder.header("Authorization", "Bearer $it") }
                    }
                    chain.proceed(builder.build())
                }
                .addInterceptor(logging)
                .dns(object : okhttp3.Dns {
                    override fun lookup(hostname: String): List<InetAddress> {
                        val all = okhttp3.Dns.SYSTEM.lookup(hostname)
                        // IPv4 优先，避免 IPv6 Happy Eyeballs 延迟
                        val ipv4 = all.filter { it is Inet4Address }
                        val ipv6 = all.filter { it !is Inet4Address }
                        return ipv4 + ipv6
                    }
                })
                .build()
        }
        return okHttpClient!!
    }

    private fun getRetrofit(): Retrofit {
        if (retrofit == null) {
            val gson = GsonBuilder()
                .setLenient()
                .create()

            retrofit = Retrofit.Builder()
                .baseUrl("$serverUrl/api/v1/")
                .client(getOkHttpClient())
                .addConverterFactory(GsonConverterFactory.create(gson))
                .build()
        }
        return retrofit!!
    }

    /** 获取流代理 URL */
    fun getStreamProxyUrl(channelId: Long): String {
        return "$serverUrl/api/v1/stream/proxy/$channelId"
    }

    /** 获取回看流 URL */
    fun getCatchupUrl(channelId: Long, startTimeUnix: Long, endTimeUnix: Long): String {
        return "$serverUrl/api/v1/stream/catchup/$channelId?start=$startTimeUnix&end=$endTimeUnix"
    }

    /**
     * 获取直连模式下的回看 URL
     * 直连模式下，服务端不再返回 302 重定向，而是返回 JSON 格式的回看 URL
     * 客户端需要先请求该接口获取实际的回看 URL，再直接播放
     */
    suspend fun fetchDirectCatchupUrl(channelId: Long, startTimeUnix: Long, endTimeUnix: Long): String {
        val url = getCatchupUrl(channelId, startTimeUnix, endTimeUnix)
        return withContext(Dispatchers.IO) {
            val request = Request.Builder().url(url).build()
            getOkHttpClient().newCall(request).execute().use { response ->
                val body = response.body?.string() ?: throw Exception("回看URL获取失败：空响应")
                val json = JSONObject(body)
                json.getString("url")
            }
        }
    }
}
