// ========================================
// Android ApiClient.kt — 修改 getStreamProxyUrl
// 使用 Header 传递 Token 而非 URL Query
// ========================================

package com.tvplayer.app.data.api

import com.tvplayer.app.Prefs
import com.tvplayer.app.TVPlayerApp
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory
import java.util.concurrent.TimeUnit

object ApiClient {

    private const val DEFAULT_BASE_URL = "http://10.0.2.2:9527/api/v1/"

    private var baseUrl: String = DEFAULT_BASE_URL
    private var retrofit: Retrofit? = null
    private var api: TVPlayerApi? = null

    fun init(serverUrl: String) {
        baseUrl = if (serverUrl.endsWith("/")) "$serverUrl/api/v1/" else "$serverUrl/api/v1/"
        retrofit = null
        api = null
    }

    fun getApi(): TVPlayerApi {
        if (api == null) {
            val logging = HttpLoggingInterceptor().apply {
                level = HttpLoggingInterceptor.Level.BASIC
            }

            val authInterceptor = Interceptor { chain ->
                val original = chain.request()
                val skipAuth = original.url.encodedPath.contains("client/register") ||
                               original.url.encodedPath.contains("client/verify")

                val request = if (!skipAuth) {
                    val prefs = TVPlayerApp.instance.getSharedPreferences(Prefs.FILE, 0)
                    val token = prefs.getString(Prefs.KEY_ACCESS_TOKEN, null)
                    if (token != null) {
                        original.newBuilder()
                            .header("X-Client-Token", token)
                            .header("Authorization", "Bearer $token")
                            .build()
                    } else {
                        original
                    }
                } else {
                    original
                }

                chain.proceed(request)
            }

            val client = OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .writeTimeout(15, TimeUnit.SECONDS)
                .addInterceptor(authInterceptor)
                .addInterceptor(logging)
                .build()

            val rf = Retrofit.Builder()
                .baseUrl(baseUrl)
                .client(client)
                .addConverterFactory(GsonConverterFactory.create())
                .build()
            retrofit = rf
            api = rf.create(TVPlayerApi::class.java)
        }
        return api ?: throw IllegalStateException("API not initialized")
    }

    // ★ 修改：getStreamProxyUrl 不再在 URL 中暴露 token
    // Token 通过 OkHttp Interceptor 自动附加到 Header 中
    fun getStreamProxyUrl(channelId: Long): String {
        val serverBase = baseUrl
            .removeSuffix("/")
            .removeSuffix("/api/v1")
            .removeSuffix("/api/v1/")
        return "$serverBase/api/v1/stream/proxy/$channelId"
    }

    fun getBaseUrl(): String = baseUrl

    fun reset() {
        retrofit = null
        api = null
    }
}
