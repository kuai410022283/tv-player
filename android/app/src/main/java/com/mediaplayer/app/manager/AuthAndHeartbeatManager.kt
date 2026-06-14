package com.mediaplayer.app.manager

import android.os.Handler
import android.os.Looper
import com.mediaplayer.app.data.api.ClientAuthManager
import com.mediaplayer.app.data.model.VerifyResponse
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

class AuthAndHeartbeatManager(
    private val authManager: ClientAuthManager,
    private val coroutineScope: CoroutineScope,
    private val onAuthSuccess: (VerifyResponse) -> Unit,
    private val onAuthWaiting: (String, Boolean) -> Unit
) {
    private val heartbeatHandler = Handler(Looper.getMainLooper())
    private var heartbeatRunnable: Runnable? = null
    
    private val authPollHandler = Handler(Looper.getMainLooper())
    private var authPollRunnable: Runnable? = null

    fun checkAuthAndLoad() {
        coroutineScope.launch {
            if (authManager.isApproved()) {
                authManager.verify().onSuccess { resp ->
                    if (resp != null) {
                        onAuthSuccess(resp)
                        startHeartbeat()
                    } else doRegister()
                }.onFailure { doRegister() }
            } else {
                doRegister()
            }
        }
    }

    private fun doRegister() {
        coroutineScope.launch {
            onAuthWaiting("正在注册设备...", false)
            authManager.register().onSuccess { result ->
                when (result.status) {
                    "approved" -> {
                        val resp = VerifyResponse(
                            clientId = result.clientId,
                            name = "",
                            announcement = result.announcement,
                            announcementInterval = result.announcementInterval,
                            enableLog = false,
                            startupMediaEnabled = result.startupMediaEnabled,
                            startupMedia = result.startupMedia,
                            startupMediaType = result.startupMediaType,
                            startupDuration = result.startupDuration,
                            startupSkipAfter = result.startupSkipAfter
                        )
                        onAuthSuccess(resp)
                        startHeartbeat()
                    }
                    "pending" -> {
                        onAuthWaiting("设备已注册，等待管理员审批...\n\n设备ID: ${authManager.getDeviceId()}", false)
                        startAuthPolling()
                    }
                    "rejected" -> onAuthWaiting("设备注册被拒绝\n请联系管理员", false)
                    "banned" -> onAuthWaiting("设备已被封禁\n请联系管理员", false)
                }
            }.onFailure { e ->
                onAuthWaiting("注册失败: ${e.message}\n\n请检查服务器地址", true)
            }
        }
    }

    private fun startAuthPolling() {
        val runnable = object : Runnable {
            override fun run() {
                coroutineScope.launch {
                    authManager.checkStatus().onSuccess { status ->
                        if (status == "approved") { 
                            authManager.verify().onSuccess { resp ->
                                if (resp != null) {
                                    onAuthSuccess(resp)
                                    startHeartbeat()
                                }
                            }
                            return@launch 
                        }
                        authPollRunnable?.let { authPollHandler.postDelayed(it, 10000) }
                    }.onFailure { authPollRunnable?.let { authPollHandler.postDelayed(it, 15000) } }
                }
            }
        }
        authPollRunnable = runnable
        authPollHandler.postDelayed(runnable, 10000)
    }

    private fun startHeartbeat() {
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        val runnable = object : Runnable {
            override fun run() {
                coroutineScope.launch {
                    try {
                        authManager.verify().onSuccess { resp ->
                            if (resp != null) {
                                // 验证成功后，将最新的服务端配置同步回 UI，确保持续热更新
                                onAuthSuccess(resp)
                            }
                        }
                    } catch (_: Exception) {}
                }
                heartbeatHandler.postDelayed(this, 3 * 60 * 1000) // 每3分钟心跳
            }
        }
        heartbeatRunnable = runnable
        heartbeatHandler.postDelayed(runnable, 3 * 60 * 1000)
    }

    fun onDestroy() {
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        authPollRunnable?.let { authPollHandler.removeCallbacks(it) }
    }
}
