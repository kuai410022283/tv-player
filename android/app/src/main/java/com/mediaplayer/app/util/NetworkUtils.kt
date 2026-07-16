package com.mediaplayer.app.util

import java.net.Inet4Address
import java.net.NetworkInterface
import java.net.SocketException

object NetworkUtils {

    data class InterfaceInfo(
        val name: String,
        val displayName: String,
        val ip: String,
        val type: String // 有线 / WiFi / 其他
    )

    /**
     * 扫描所有可用的网络接口
     */
    fun getAvailableInterfaces(): List<InterfaceInfo> {
        val result = mutableListOf<InterfaceInfo>()
        try {
            val en = NetworkInterface.getNetworkInterfaces()
            while (en.hasMoreElements()) {
                val intf = en.nextElement()
                if (!intf.isUp || intf.isLoopback) continue
                val enumIpAddr = intf.inetAddresses
                while (enumIpAddr.hasMoreElements()) {
                    val inetAddress = enumIpAddr.nextElement()
                    if (!inetAddress.isLoopbackAddress && inetAddress is Inet4Address) {
                        val ip = inetAddress.hostAddress ?: continue
                        val name = intf.name.lowercase()
                        val type = when {
                            name.startsWith("eth") || name.startsWith("en") || name.contains("ether") -> "有线"
                            name.startsWith("wlan") || name.startsWith("wl") -> "WiFi"
                            else -> "其他"
                        }
                        result.add(InterfaceInfo(
                            name = intf.name,
                            displayName = "$type (${intf.name})",
                            ip = ip,
                            type = type
                        ))
                    }
                }
            }
        } catch (ex: SocketException) {
            ex.printStackTrace()
        }
        // 排序：有线 > WiFi > 其他
        return result.sortedBy { item ->
            when (item.type) {
                "有线" -> 0
                "WiFi" -> 1
                else -> 2
            }
        }
    }

    /**
     * 获取本地 IPv4 地址，优先级：
     * 1. 有线网卡（eth/en/ether）
     * 2. 无线网卡（wlan/wl）
     * 3. 其他（tun、ppp 等）
     */
    fun getLocalIpAddress(): String? {
        val interfaces = getAvailableInterfaces()
        return interfaces.firstOrNull()?.ip
    }
}
