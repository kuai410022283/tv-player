package com.mediaplayer.app.util

import android.annotation.SuppressLint
import okhttp3.ConnectionPool
import okhttp3.OkHttpClient
import java.net.Authenticator
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.PasswordAuthentication
import java.net.Proxy
import java.net.URI
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager

object PlayerNetworkHelper {

    private var playerOkHttpClient: OkHttpClient? = null

    // 按 "socks5://host:port" 缓存带代理的 OkHttpClient，避免每次切台重建
    private val proxyClientCache = mutableMapOf<String, OkHttpClient>()

    /**
     * 获取专用于播放器的 OkHttpClient。
     * 配置了：
     * 1. 较长的超时时间（应对弱网）
     * 2. 信任所有 SSL 证书（应对老旧 Android TV 设备 Let's Encrypt 根证书过期的问题）
     * 3. 禁用重定向（通常在 StreamResolver 已经做了解析，或者交给 ExoPlayer 自己处理）
     * 4. 连接池复用（减少NAT压力，优化光猫性能）
     */
    fun getPlayerOkHttpClient(): OkHttpClient {
        if (playerOkHttpClient == null) {
            // 连接池：保持5个空闲连接，存活5分钟
            // 减少新建连接数，降低光猫NAT转换压力
            val connectionPool = ConnectionPool(5, 5, TimeUnit.MINUTES)

            val builder = OkHttpClient.Builder()
                .connectTimeout(15, TimeUnit.SECONDS)
                .readTimeout(30, TimeUnit.SECONDS)
                .writeTimeout(15, TimeUnit.SECONDS)
                // 允许跨协议重定向等在 ExoPlayer 层面配置，但在底层 OkHttp 可以放开基本限制
                .followRedirects(true)
                .followSslRedirects(true)
                .retryOnConnectionFailure(true)
                // 连接池复用
                .connectionPool(connectionPool)
                // DNS缓存 + IPv4 优先：减少DNS查询，避免 IPv6 Happy Eyeballs 延迟
                .dns(object : okhttp3.Dns {
                    private val cache = mutableMapOf<String, List<InetAddress>>()
                    override fun lookup(hostname: String): List<InetAddress> {
                        return cache.getOrPut(hostname) {
                            val all = okhttp3.Dns.SYSTEM.lookup(hostname)
                            // IPv4 优先，避免 IPv6 Happy Eyeballs 延迟
                            val ipv4 = all.filter { it is Inet4Address }
                            val ipv6 = all.filter { it !is Inet4Address }
                            ipv4 + ipv6
                        }
                    }
                })

            try {
                // 创建一个不验证证书链的信任管理器
                val trustAllCerts = arrayOf<TrustManager>(
                    @SuppressLint("CustomX509TrustManager")
                    object : X509TrustManager {
                        @SuppressLint("TrustAllX509TrustManager")
                        override fun checkClientTrusted(chain: Array<X509Certificate>, authType: String) {}

                        @SuppressLint("TrustAllX509TrustManager")
                        override fun checkServerTrusted(chain: Array<X509Certificate>, authType: String) {}

                        override fun getAcceptedIssuers(): Array<X509Certificate> = arrayOf()
                    }
                )

                // 安装信任所有证书的 trust manager
                val sslContext = SSLContext.getInstance("TLS")
                sslContext.init(null, trustAllCerts, SecureRandom())
                
                // 创建一个忽略主机名验证的 HostnameVerifier
                builder.sslSocketFactory(sslContext.socketFactory, trustAllCerts[0] as X509TrustManager)
                builder.hostnameVerifier { _, _ -> true }
                
            } catch (e: Exception) {
                RemoteLogger.e("PlayerNetworkHelper", "Failed to setup unsafe SSL context", e)
            }

            playerOkHttpClient = builder.build()
        }
        return playerOkHttpClient!!
    }

    /**
     * 获取带 SOCKS5 代理的 OkHttpClient。
     * - proxyType 为空或 "none" → 返回共享直连单例
     * - proxyType 为 "socks5" → 返回带代理的客户端（带缓存）
     * - 代理解析失败 → 降级为直连单例，不阻断播放
     */
    fun getPlayerOkHttpClient(proxyType: String?, proxyUrl: String?): OkHttpClient {
        if (proxyType.isNullOrEmpty() || proxyType == "none" || proxyUrl.isNullOrEmpty()) {
            return getPlayerOkHttpClient()
        }
        val cacheKey = "$proxyType:$proxyUrl"
        return proxyClientCache.getOrPut(cacheKey) {
            val proxy = parseProxy(proxyType, proxyUrl)
            if (proxy != null) {
                RemoteLogger.i("PlayerNetworkHelper", "Creating SOCKS5 proxy client for: $proxyUrl")
                getPlayerOkHttpClient().newBuilder().proxy(proxy).build()
            } else {
                RemoteLogger.w("PlayerNetworkHelper", "Failed to parse proxy '$proxyUrl', falling back to direct")
                getPlayerOkHttpClient()
            }
        }
    }

    /**
     * 解析 SOCKS5 代理地址，返回 java.net.Proxy。
     * 格式：socks5://host:port 或 socks5://user:pass@host:port
     * 解析失败返回 null（降级直连）。
     */
    private fun parseProxy(type: String, url: String): Proxy? {
        if (type != "socks5") return null
        return try {
            val uri = URI(url)
            val host = uri.host ?: return null
            val port = if (uri.port > 0) uri.port else 1080
            // SOCKS5 认证（如果有 user info）
            if (uri.userInfo != null) {
                val parts = uri.userInfo.split(":", limit = 2)
                val user = parts[0]
                val pass = parts.getOrElse(1) { "" }
                Authenticator.setDefault(object : Authenticator() {
                    override fun getPasswordAuthentication(): PasswordAuthentication {
                        return PasswordAuthentication(user, pass.toCharArray())
                    }
                })
            }
            Proxy(Proxy.Type.SOCKS, InetSocketAddress.createUnresolved(host, port))
        } catch (e: Exception) {
            RemoteLogger.e("PlayerNetworkHelper", "Failed to parse SOCKS5 proxy: $url", e)
            null
        }
    }
}
