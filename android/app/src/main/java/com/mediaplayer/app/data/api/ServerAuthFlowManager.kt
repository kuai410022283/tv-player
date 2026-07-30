package com.mediaplayer.app.data.api

import android.content.Context
import android.os.Handler
import android.os.Looper
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.data.model.ClientRegisterResp
import com.mediaplayer.app.data.model.VerifyResponse
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch
import kotlinx.coroutines.delay
import kotlinx.coroutines.withTimeout

/**
 * 服务器认证流程管理器
 * 负责主备服务器切换、Token 管理、维护模式检测
 */
class ServerAuthFlowManager(
    private val context: Context,
    private val scope: CoroutineScope,
    private val authManager: ClientAuthManager
) {
    /**
     * 认证流程回调接口
     */
    interface Callback {
        /** 状态更新 */
        fun onStatusUpdate(message: String, showQr: Boolean = true)
        /** 认证成功 */
        fun onSuccess(resp: VerifyResponse)
        /** 等待审批 */
        fun onPending(deviceId: String)
        /** 注册被拒绝 */
        fun onRejected(message: String)
        /** 设备被封禁 */
        fun onBanned(message: String)
        /** 所有服务器均失败 */
        fun onAllFailed()
        /** 重试已调度 */
        fun onRetryScheduled(delayMs: Long)
    }

    private val handler = Handler(Looper.getMainLooper())
    private var retryRunnable: Runnable? = null
    private var callback: Callback? = null

    fun setCallback(callback: Callback) {
        this.callback = callback
    }

    /**
     * 启动认证流程
     */
    fun startAuthFlow() {
        cancelRetry()

        scope.launch {
            if (authManager.isApproved()) {
                verifyServers()
            } else {
                registerToServers()
            }
        }
    }

    /**
     * 取消重试
     */
    fun cancelRetry() {
        retryRunnable?.let { handler.removeCallbacks(it) }
        retryRunnable = null
    }

    /**
     * 验证所有服务器（优化后的逻辑）
     * 增加了每台服务器的重试机制，以应对反向代理/内网穿透场景的瞬时网络波动。
     */
    private suspend fun verifyServers() {
        val candidates = getCandidateServers()
        var maintenanceResp: VerifyResponse? = null

        for ((index, serverUrl) in candidates.withIndex()) {
            val label = if (index == 0) "主服务器" else "备用服务器 $index"
            
            // 每台服务器最多重试 2 次，应对反向代理/内网穿透瞬时连接失败
            var retryCount = 0
            val maxRetries = 2
            while (retryCount < maxRetries) {
                if (retryCount > 0) {
                    callback?.onStatusUpdate("正在重试$label (${retryCount+1}/$maxRetries)...")
                    delay(1000) // 重试前等待 1 秒
                } else {
                    callback?.onStatusUpdate("正在验证$label ...")
                }

                ApiClient.init(serverUrl)

                // 动态超时：首次 15s，重试时增加到 20s（慢速反向代理场景）
                val timeoutMs = if (retryCount == 0) 15_000L else 20_000L
                val attempt = try {
                    withTimeout(timeoutMs) { authManager.verify() }
                } catch (_: Exception) {
                    Result.failure(Exception("连接超时"))
                }

                if (attempt.isSuccess) {
                    val resp = attempt.getOrNull()
                    if (resp != null) {
                        if (resp.globalMaintenance && !resp.isTester) {
                            // 该服务器处于维护模式，记录下来并尝试下一个备用服务器
                            maintenanceResp = resp
                            break
                        } else {
                            // 验证成功，尝试注册到其他服务器获取 Token
                            if (index != 0) {
                                registerToOtherServers(candidates, index)
                            }
                            callback?.onSuccess(resp)
                            return
                        }
                    } else {
                        // Token 无效（非网络错误），尝试重新注册到当前服务器
                        val registerResult = registerToServer(serverUrl, index)
                        if (registerResult != null) {
                            callback?.onSuccess(registerResult)
                            return
                        }
                    }
                }
                // attempt.isFailure → 增加重试计数，继续重试当前服务器
                retryCount++
            }
            // 当前服务器重试耗尽，继续尝试下一个服务器（不清空 Token）
        }

        // 所有服务器验证失败
        if (maintenanceResp != null) {
            callback?.onSuccess(maintenanceResp)
        } else {
            registerToServers()
        }
    }

    /**
     * 注册到所有服务器（优化后的逻辑，不清空 Token）
     * 增加了每台服务器的重试机制，以应对反向代理/内网穿透场景的瞬时网络波动。
     */
    private suspend fun registerToServers() {
        val candidates = getCandidateServers()
        var maintenanceResult: ClientRegisterResp? = null

        callback?.onStatusUpdate("正在注册设备...")

        for ((index, serverUrl) in candidates.withIndex()) {
            val label = if (index == 0) "主服务器" else "备用服务器 $index"

            // 每台服务器最多重试 2 次
            var retryCount = 0
            val maxRetries = 2
            while (retryCount < maxRetries) {
                if (retryCount > 0) {
                    callback?.onStatusUpdate("正在重试连接$label (${retryCount+1}/$maxRetries)...")
                    delay(1000)
                } else {
                    callback?.onStatusUpdate("正在连接$label ...")
                }

                ApiClient.init(serverUrl)

                val timeoutMs = if (retryCount == 0) 15_000L else 20_000L
                val attempt = try {
                    withTimeout(timeoutMs) { authManager.register() }
                } catch (_: Exception) {
                    Result.failure(Exception("连接超时"))
                }

                if (attempt.isSuccess) {
                    val result = attempt.getOrThrow()
                    when (result.status) {
                        "approved" -> {
                            if (result.globalMaintenance && !result.isTester) {
                                maintenanceResult = result
                                break
                            } else {
                                callback?.onSuccess(convertToVerifyResponse(result))
                                return
                            }
                        }
                        "pending" -> {
                            callback?.onPending(authManager.getDeviceId())
                            return
                        }
                        "rejected" -> {
                            callback?.onRejected("设备注册被拒绝\n请联系管理员")
                            return
                        }
                        "banned" -> {
                            callback?.onBanned("设备已被封禁\n请联系管理员")
                            return
                        }
                    }
                }
                retryCount++
            }
            // attempt.isFailure → 继续尝试下一台服务器
        }

        if (maintenanceResult != null) {
            callback?.onSuccess(convertToVerifyResponse(maintenanceResult))
        } else {
            callback?.onAllFailed()
            scheduleRetry()
        }
    }

    /**
     * 注册到单个服务器（用于 Token 刷新）
     */
    private suspend fun registerToServer(serverUrl: String, index: Int): VerifyResponse? {
        ApiClient.init(serverUrl)
        // 注意：这里不再调用 authManager.clearAuth()

        val attempt = try {
            withTimeout(15_000L) { authManager.register() }
        } catch (_: Exception) {
            Result.failure(Exception("连接超时"))
        }

        if (attempt.isSuccess) {
            val result = attempt.getOrThrow()
            if (result.status == "approved" && !result.globalMaintenance) {
                return convertToVerifyResponse(result)
            }
        }
        return null
    }

    /**
     * 尝试注册到其他服务器（用于 Token 同步）
     */
    private suspend fun registerToOtherServers(candidates: List<String>, excludeIndex: Int) {
        for ((index, serverUrl) in candidates.withIndex()) {
            if (index == excludeIndex) continue
            registerToServer(serverUrl, index)
        }
    }

    /**
     * 调度重试
     */
    private fun scheduleRetry(delayMs: Long = 15_000L) {
        callback?.onRetryScheduled(delayMs)
        retryRunnable = Runnable { startAuthFlow() }
        handler.postDelayed(retryRunnable!!, delayMs)
    }

    /**
     * 获取候选服务器列表
     */
    private fun getCandidateServers(): List<String> {
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        val serverListJson = prefs.getString(Prefs.KEY_SERVER_URLS, null)
        val serverList = if (serverListJson != null) {
            try {
                // 尝试新格式（ServerEntry 数组）
                val arr = com.google.gson.Gson().fromJson(serverListJson, Array<com.mediaplayer.app.data.model.ServerEntry>::class.java)
                arr.toList().filter { it.url.isNotEmpty() }.map { it.url }
            } catch (e: Exception) {
                try {
                    // 兼容旧格式（纯字符串数组）
                    val arr = com.google.gson.Gson().fromJson(serverListJson, Array<String>::class.java)
                    arr.toList().filter { it.isNotEmpty() }
                } catch (e2: Exception) {
                    emptyList()
                }
            }
        } else {
            emptyList()
        }
        val defaultUrl = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: Prefs.DEFAULT_SERVER_URL
        val rawCandidates = serverList.ifEmpty { listOf(defaultUrl) }
        
        // 智能洗平：如果读取到的历史数据是 Base64 脏数据，自动转换为明文 http/s 地址，实现无缝升级
        var candidates = rawCandidates.map { url ->
            val decoded = try {
                val bytes = android.util.Base64.decode(url, android.util.Base64.DEFAULT)
                String(bytes, Charsets.UTF_8).trim()
            } catch (_: Exception) {
                null
            }
            if (decoded != null && (decoded.startsWith("http://", ignoreCase = true) || decoded.startsWith("https://", ignoreCase = true))) {
                decoded
            } else {
                url.trim()
            }
        }
        
        // 核心修复：不应该“只返回首选线路”导致一条路走到黑。
        // 如果用户锁定了首选线路，应该将其“提拔”到列表最前面，依然保留后续备用节点作为防线。
        // 这样既实现了秒进首选，又保证了遇到该线路维护/宕机时能按顺序向下轮询兜底。
        val preferredIndex = prefs.getInt(Prefs.KEY_PREFERRED_SERVER_INDEX, -1)
        if (preferredIndex in candidates.indices) {
            val preferredUrl = candidates[preferredIndex]
            val others = candidates.filterIndexed { index, _ -> index != preferredIndex }
            candidates = listOf(preferredUrl) + others
        }
        
        return candidates
    }

    /**
     * 将 ClientRegisterResp 转换为 VerifyResponse
     */
    private fun convertToVerifyResponse(resp: ClientRegisterResp): VerifyResponse {
        return VerifyResponse(
            clientId = resp.clientId,
            serverName = resp.serverName,
            announcement = resp.announcement,
            announcementInterval = resp.announcementInterval,
            enableLog = resp.enableLog,
            startupMediaEnabled = resp.startupMediaEnabled,
            startupMedia = resp.startupMedia,
            startupMediaType = resp.startupMediaType,
            startupDuration = resp.startupDuration,
            startupSkipAfter = resp.startupSkipAfter,
            globalMaintenance = resp.globalMaintenance,
            backupServers = resp.backupServers,
            isTester = resp.isTester
        )
    }
}
