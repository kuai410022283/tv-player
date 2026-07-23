package com.mediaplayer.app.util

import android.content.Context
import com.mediaplayer.app.MediaPlayerApp
import com.mediaplayer.app.Prefs
import java.net.Inet4Address
import java.net.Inet6Address
import java.net.NetworkInterface
import java.net.SocketException

object NetworkUtils {

    data class InterfaceInfo(
        val name: String,
        val displayName: String,
        val ip: String,            // 原始 IP 地址（如 192.168.1.100 或 240e:xxx:xxx）
        val formattedIp: String,   // 适合 URL 的 IP 地址（如 192.168.1.100 或 [240e:xxx:xxx]）
        val isIpv6: Boolean,
        val type: String           // 有线 / WiFi / 其他
    )

    /**
     * 扫描所有可用网卡，提取包含 IPv4 和全局单播 IPv6 地址在内的全量网络接口信息。
     */
    fun getAvailableInterfaces(): List<InterfaceInfo> {
        val result = mutableListOf<InterfaceInfo>()
        try {
            val en = NetworkInterface.getNetworkInterfaces() ?: return emptyList()
            while (en.hasMoreElements()) {
                val intf = en.nextElement() ?: continue
                // 仅扫描正常 UP 状态且非 Loopback 的接口
                if (!intf.isUp || intf.isLoopback) continue

                val name = (intf.name ?: "").lowercase()
                val type = when {
                    name.startsWith("eth") || name.startsWith("en") || name.contains("ether") ||
                    name.startsWith("lan") || name.startsWith("net") -> "有线"
                    name.startsWith("wlan") || name.startsWith("wl") || name.startsWith("ap") -> "WiFi"
                    else -> "其他"
                }

                val enumIpAddr = intf.inetAddresses ?: continue
                while (enumIpAddr.hasMoreElements()) {
                    val inetAddress = enumIpAddr.nextElement() ?: continue
                    val rawIpWithScope = inetAddress.hostAddress ?: continue
                    val rawIp = rawIpWithScope.split("%")[0].trim() // 清除 IPv6 Scope ID

                    // 严密排除 127.0.0.1 (127.x.x.x) 和 ::1 环回地址
                    if (inetAddress.isLoopbackAddress || rawIp.startsWith("127.") || rawIp == "::1") continue

                    if (inetAddress is Inet4Address) {
                        result.add(InterfaceInfo(
                            name = intf.name ?: "",
                            displayName = "$type (${intf.name ?: ""})",
                            ip = rawIp,
                            formattedIp = rawIp,
                            isIpv6 = false,
                            type = type
                        ))
                    } else if (inetAddress is Inet6Address) {
                        // 过滤 链路本地地址 (fe80::) 和 Loopback 地址，保留全局单播 IPv6 地址
                        if (!inetAddress.isLinkLocalAddress && !inetAddress.isLoopbackAddress) {
                            result.add(InterfaceInfo(
                                name = intf.name ?: "",
                                displayName = "$type IPv6 (${intf.name ?: ""})",
                                ip = rawIp,
                                formattedIp = "[$rawIp]",
                                isIpv6 = true,
                                type = type
                            ))
                        }
                    }
                }
            }
        } catch (ex: Throwable) {
            ex.printStackTrace()
        }

        // 读取用户设置的“网络 DNS 策略”
        val dnsPolicy = try {
            val prefs = MediaPlayerApp.instance.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
            prefs.getInt(Prefs.KEY_DNS_POLICY, Prefs.DNS_POLICY_AUTO)
        } catch (_: Exception) {
            Prefs.DNS_POLICY_AUTO
        }

        // 排序规则：
        // 1. 网卡类型：有线 (0) > WiFi (1) > 其他 (2)
        // 2. 协议偏好：
        //    - 若设为 优先 IPv6 (2)：IPv6 排在前
        //    - 自动 / 优先 IPv4：IPv4 排在前
        return result.sortedWith(Comparator { a, b ->
            val typeScoreA = when (a.type) { "有线" -> 0; "WiFi" -> 1; else -> 2 }
            val typeScoreB = when (b.type) { "有线" -> 0; "WiFi" -> 1; else -> 2 }
            if (typeScoreA != typeScoreB) return@Comparator typeScoreA.compareTo(typeScoreB)

            val ipScoreA = if (dnsPolicy == Prefs.DNS_POLICY_IPV6_FIRST) (if (a.isIpv6) 0 else 1) else (if (a.isIpv6) 1 else 0)
            val ipScoreB = if (dnsPolicy == Prefs.DNS_POLICY_IPV6_FIRST) (if (b.isIpv6) 0 else 1) else (if (b.isIpv6) 1 else 0)
            ipScoreA.compareTo(ipScoreB)
        })
    }

    /**
     * 获取最优先的本地 IP 地址（格式化后的，IPv6 会自动添加 []）。
     */
    fun getLocalIpAddress(): String? {
        val interfaces = getAvailableInterfaces()
        return interfaces.firstOrNull()?.formattedIp
    }

    /**
     * 获取所有可用的格式化访问 URL 列表。
     */
    fun getAvailableWebUrls(port: Int): List<String> {
        return getAvailableInterfaces().map { info ->
            "http://${info.formattedIp}:$port/"
        }.distinct()
    }
}
