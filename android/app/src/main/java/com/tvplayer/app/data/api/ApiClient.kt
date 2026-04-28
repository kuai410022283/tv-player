package com.tvplayer.app.data.api

import com.google.gson.GsonBuilder
import okhttp3.OkHttpClient
import okhttp3.logging.HttpLoggingInterceptor
import retrofit2.Retrofit
import retrofit2.converter.gson.GsonConverterFactory
import java.util.concurrent.TimeUnit

/**
 * API 客户端单例 —— 管理 Retrofit 实例和服务器地址。
 * 使用前必须调用 [init] 设置服务器地址。
 */
object ApiClient {

    private var serverUrl: String = "http://10.0.2.2:9527"
    private var retrofit: Retrofit? = null
    private var apiService: ApiService? = null

    /** 当前使用的 token（由 ClientAuthManager 设置） */
    var accessToken: String? = null

    fun init(url: String) {
        val normalized = url.trimEnd('/')
        if (normalized == serverUrl && retrofit != null) return
        serverUrl = normalized
        retrofit = null
        apiService = null
    }

    fun reset() {
        retrofit = null
        apiService = null
        accessToken = null
    }

    fun getServerUrl(): String = serverUrl

    fun getService(): ApiService {
        if (apiService == null) {
            apiService = getRetrofit().create(ApiService::class.java)
        }
        return apiService!!
    }

    private fun getRetrofit(): Retrofit {
        if (retrofit == null) {
            val logging = HttpLoggingInterceptor().apply {
                level = HttpLoggingInterceptor.Level.BASIC
            }

            val client = OkHttpClient.Builder()
                .connectTimeout(10, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .writeTimeout(15, TimeUnit.SECONDS)
                .addInterceptor { chain ->
                    val original = chain.request()
                    val builder = original.newBuilder()
                    accessToken?.let { builder.header("Authorization", "Bearer $it") }
                    chain.proceed(builder.build())
                }
                .addInterceptor(logging)
                .build()

            val gson = GsonBuilder()
                .setLenient()
                .create()

            retrofit = Retrofit.Builder()
                .baseUrl("$serverUrl/api/v1/")
                .client(client)
                .addConverterFactory(GsonConverterFactory.create(gson))
                .build()
        }
        return retrofit!!
    }

    /** 获取流代理 URL（带 token 参数，兼容旧播放器） */
    fun getStreamProxyUrl(channelId: Long): String {
        val token = accessToken ?: ""
        return "$serverUrl/api/v1/stream/proxy/$channelId?token=$token"
    }
}
