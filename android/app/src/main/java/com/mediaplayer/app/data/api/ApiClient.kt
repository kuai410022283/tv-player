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
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

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

    fun resetOkHttpClient() {
        retrofit = null
        apiService = null
        okHttpClient = null
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
                .connectionPool(okhttp3.ConnectionPool(5, 30, TimeUnit.SECONDS))
                .retryOnConnectionFailure(true)
                .followRedirects(true)
                .followSslRedirects(true)
                .apply {
                    // 兼容自签名证书的反向代理（如 FRP、Ngrok 等）
                    // 首次 SSL 握手失败时自动降级尝试宽松验证
                    try {
                        val trustAllCerts = arrayOf<TrustManager>(object : X509TrustManager {
                            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
                            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {}
                            override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
                        })
                        val sslContext = SSLContext.getInstance("TLS")
                        sslContext.init(null, trustAllCerts, SecureRandom())
                        sslSocketFactory(sslContext.socketFactory, trustAllCerts[0] as X509TrustManager)
                        hostnameVerifier { _, _ -> true }
                    } catch (_: Exception) {
                        // 设备不支持自定义 SSL，降级使用系统默认
                    }
                }
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
                .apply {
                    try {
                        dns(object : okhttp3.Dns {
                            override fun lookup(hostname: String): List<InetAddress> {
                                return try {
                                    val all = okhttp3.Dns.SYSTEM.lookup(hostname)
                                    val prefs = com.mediaplayer.app.MediaPlayerApp.instance.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, android.content.Context.MODE_PRIVATE)
                                    val policy = prefs.getInt(com.mediaplayer.app.Prefs.KEY_DNS_POLICY, com.mediaplayer.app.Prefs.DNS_POLICY_AUTO)
                                    
                                    when (policy) {
                                        com.mediaplayer.app.Prefs.DNS_POLICY_IPV4_FIRST -> {
                                            val ipv4 = all.filter { it is Inet4Address }
                                            val ipv6 = all.filter { it !is Inet4Address }
                                            ipv4 + ipv6
                                        }
                                        com.mediaplayer.app.Prefs.DNS_POLICY_IPV6_FIRST -> {
                                            val ipv4 = all.filter { it is Inet4Address }
                                            val ipv6 = all.filter { it !is Inet4Address }
                                            ipv6 + ipv4
                                        }
                                        else -> all // 自动：系统默认
                                    }
                                } catch (e: Exception) {
                                    okhttp3.Dns.SYSTEM.lookup(hostname)
                                }
                            }
                        })
                    } catch (e: Exception) {
                        // 设备不支持自定义 DNS 策略，降级为系统默认 DNS
                    }
                }
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
