package com.tvplayer.app.data.api

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

            // Auth interceptor: 自动附加 client token
            val authInterceptor = Interceptor { chain ->
                val original = chain.request()

                // 注册和验证接口不需要 token
                val skipAuth = original.url.encodedPath.contains("client/register") ||
                               original.url.encodedPath.contains("client/verify")

                val request = if (!skipAuth) {
                    val prefs = TVPlayerApp.instance.getSharedPreferences("tvplayer_auth", 0)
                    val token = prefs.getString("access_token", null)
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

            retrofit = Retrofit.Builder()
                .baseUrl(baseUrl)
                .client(client)
                .addConverterFactory(GsonConverterFactory.create())
                .build()

            api = retrofit!!.create(TVPlayerApi::class.java)
        }
        return api!!
    }

    fun getStreamProxyUrl(channelId: Long): String {
        val base = baseUrl.removeSuffix("/api/v1/")
        // Stream proxy 需要附带 token
        val prefs = TVPlayerApp.instance.getSharedPreferences("tvplayer_auth", 0)
        val token = prefs.getString("access_token", "") ?: ""
        return "$base/api/v1/stream/proxy/$channelId?token=$token"
    }

    fun reset() {
        retrofit = null
        api = null
    }
}
