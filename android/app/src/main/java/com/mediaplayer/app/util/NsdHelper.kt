package com.mediaplayer.app.util

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build

/**
 * 局域网服务发现与注册帮助类 (mDNS/NSD)
 */
object NsdHelper {
    private const val SERVICE_TYPE = "_tvplayer._tcp."
    private const val TAG = "NsdHelper"

    data class DeviceInfo(val name: String, val ip: String, val port: Int)

    private var registrationListener: NsdManager.RegistrationListener? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null

    /**
     * 在电视端注册局域网服务
     * @param context Context
     * @param port 监听的端口 (NanoHTTPD 的实际端口)
     */
    fun registerService(context: Context, port: Int) {
        val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager

        val serviceInfo = NsdServiceInfo().apply {
            serviceName = Build.MODEL // 使用硬件型号作为默认名称
            serviceType = SERVICE_TYPE
            setPort(port)
        }

        registrationListener = object : NsdManager.RegistrationListener {
            override fun onServiceRegistered(NsdServiceInfo: NsdServiceInfo) {
                RemoteLogger.i(TAG, "服务注册成功: ${NsdServiceInfo.serviceName} 端口: $port")
            }

            override fun onRegistrationFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                RemoteLogger.e(TAG, "服务注册失败, 错误码: $errorCode")
            }

            override fun onServiceUnregistered(arg0: NsdServiceInfo) {
                RemoteLogger.i(TAG, "服务已注销: ${arg0.serviceName}")
            }

            override fun onUnregistrationFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                RemoteLogger.e(TAG, "服务注销失败, 错误码: $errorCode")
            }
        }

        try {
            nsdManager.registerService(
                serviceInfo, NsdManager.PROTOCOL_DNS_SD, registrationListener
            )
        } catch (e: Exception) {
            RemoteLogger.e(TAG, "注册服务异常: ${e.message}")
        }
    }

    /**
     * 注销服务
     */
    fun unregisterService(context: Context) {
        val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager
        registrationListener?.let {
            try {
                nsdManager.unregisterService(it)
            } catch (e: Exception) {
                RemoteLogger.e(TAG, "注销服务异常: ${e.message}")
            }
            registrationListener = null
        }
    }

    /**
     * 开始扫描局域网内的设备
     * @param onDeviceFound 当解析到真实的设备IP和端口时回调
     */
    fun startDiscovery(context: Context, onDeviceFound: (DeviceInfo) -> Unit, onDiscoveryStopped: () -> Unit) {
        // 【防御性处理】确保开启新扫描前，完全清理并停止上一次可能遗留的扫描
        stopDiscovery(context)
        
        val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager

        val currentListener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(regType: String) {
                RemoteLogger.i(TAG, "开始扫描设备")
            }

            override fun onServiceFound(service: NsdServiceInfo) {
                RemoteLogger.i(TAG, "发现服务: ${service.serviceName}")
                if (service.serviceType == SERVICE_TYPE) {
                    // 必须 resolve 才能获取到具体的 IP
                    try {
                        nsdManager.resolveService(service, object : NsdManager.ResolveListener {
                            override fun onResolveFailed(serviceInfo: NsdServiceInfo, errorCode: Int) {
                                RemoteLogger.e(TAG, "解析服务失败: $errorCode")
                            }

                            override fun onServiceResolved(serviceInfo: NsdServiceInfo) {
                                val host = serviceInfo.host?.hostAddress
                                val port = serviceInfo.port
                                if (host != null && host != "127.0.0.1") { // 过滤掉自己发出的广播
                                    RemoteLogger.i(TAG, "解析成功: ${serviceInfo.serviceName} - $host:$port")
                                    onDeviceFound(DeviceInfo(serviceInfo.serviceName, host, port))
                                }
                            }
                        })
                    } catch (e: Exception) {
                        RemoteLogger.e(TAG, "解析调用异常: ${e.message}")
                    }
                }
            }

            override fun onServiceLost(service: NsdServiceInfo) {
                RemoteLogger.e(TAG, "服务丢失: ${service.serviceName}")
            }

            override fun onDiscoveryStopped(serviceType: String) {
                RemoteLogger.i(TAG, "扫描已停止")
                onDiscoveryStopped()
            }

            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                RemoteLogger.e(TAG, "启动扫描失败: $errorCode")
                stopDiscovery(context)
            }

            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                RemoteLogger.e(TAG, "停止扫描失败: $errorCode")
            }
        }
        
        discoveryListener = currentListener

        try {
            nsdManager.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, currentListener)
        } catch (e: Exception) {
            RemoteLogger.e(TAG, "启动扫描调用异常: ${e.message}")
            discoveryListener = null
        }
    }

    /**
     * 停止扫描
     */
    fun stopDiscovery(context: Context) {
        val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager
        val listener = discoveryListener
        discoveryListener = null // 立即置空，避免重入或并发调用
        
        listener?.let {
            try {
                nsdManager.stopServiceDiscovery(it)
            } catch (e: IllegalArgumentException) {
                // 常见于 Listener 尚未注册或已注销
                RemoteLogger.e(TAG, "停止扫描时发生参数异常: ${e.message}")
            } catch (e: Exception) {
                RemoteLogger.e(TAG, "停止扫描异常: ${e.message}")
            }
        }
    }
}
