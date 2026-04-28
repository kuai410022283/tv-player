package com.tvplayer.app.data.api

import android.content.Context
import android.os.Build
import android.provider.Settings
import com.tvplayer.app.Prefs
import com.tvplayer.app.TVPlayerApp
import com.tvplayer.app.data.model.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import retrofit2.Response

class ClientAuthManager(private val context: Context) {

    private val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
    private val api = ApiClient.getApi()

    companion object {
        private const val KEY_TOKEN = Prefs.KEY_ACCESS_TOKEN
        private const val KEY_CLIENT_ID = Prefs.KEY_CLIENT_ID
        private const val KEY_STATUS = Prefs.KEY_CLIENT_STATUS
        private const val KEY_DEVICE_ID = Prefs.KEY_DEVICE_ID
    }

    fun getToken(): String? = prefs.getString(KEY_TOKEN, null)
    fun getClientId(): Long = prefs.getLong(KEY_CLIENT_ID, 0)
    fun getStatus(): String = prefs.getString(KEY_STATUS, "unknown") ?: "unknown"
    fun isApproved(): Boolean = getStatus() == "approved" && getToken() != null

    fun getDeviceId(): String {
        var id = prefs.getString(KEY_DEVICE_ID, null)
        if (id == null) {
            // 优先用 ANDROID_ID，稳定且恢复出厂后会变（符合预期）
            val androidId = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
            id = if (!androidId.isNullOrEmpty() && androidId != "9774d56d682e549c") {
                androidId
            } else {
                // fallback: 组合多个硬件特征
                val raw = "${Build.BOARD}:${Build.DEVICE}:${Build.MANUFACTURER}:${Build.MODEL}:${Build.SERIAL}"
                raw.hashCode().toLong().toString(16)
            }
            prefs.edit().putString(KEY_DEVICE_ID, id).apply()
        }
        return id
    }

    /**
     * 注册设备到服务器，返回注册结果
     */
    suspend fun register(): Result<RegisterResult> = withContext(Dispatchers.IO) {
        try {
            val req = ClientRegisterReq(
                name = "${Build.MANUFACTURER} ${Build.MODEL}",
                deviceId = getDeviceId(),
                deviceModel = Build.MODEL,
                deviceOs = "Android ${Build.VERSION.RELEASE}",
                appVersion = "1.0.0",
                note = ""
            )

            val res = api.registerClient(req)
            if (res.isSuccessful || res.code() == 202) {
                val body = res.body()
                if (body != null) {
                    val data = body.data
                    if (data != null) {
                        // 保存认证信息
                        prefs.edit().apply {
                            putLong(KEY_CLIENT_ID, data.clientId)
                            putString(KEY_STATUS, data.status)
                            if (data.accessToken.isNotEmpty()) {
                                putString(KEY_TOKEN, data.accessToken)
                            }
                            apply()
                        }

                        Result.success(RegisterResult(
                            status = data.status,
                            message = data.message,
                            token = data.accessToken
                        ))
                    } else {
                        Result.failure(Exception(body.message))
                    }
                } else {
                    Result.failure(Exception("空响应"))
                }
            } else {
                val errorMsg = when (res.code()) {
                    403 -> "设备已被封禁"
                    429 -> "注册请求过于频繁，请稍后再试"
                    else -> "注册失败: HTTP ${res.code()}"
                }
                if (res.code() == 403) {
                    prefs.edit().putString(KEY_STATUS, "banned").apply()
                }
                Result.failure(Exception(errorMsg))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 验证当前令牌是否有效
     */
    suspend fun verify(): Result<Boolean> = withContext(Dispatchers.IO) {
        val token = getToken() ?: return@withContext Result.success(false)
        try {
            val res = api.verifyClient("Bearer $token")
            if (res.isSuccessful && res.body()?.code == 0) {
                Result.success(true)
            } else {
                // Token 无效，清除本地状态
                prefs.edit().remove(KEY_TOKEN).apply()
                Result.success(false)
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 检查注册状态（轮询等待审批）
     */
    suspend fun checkStatus(): Result<String> = withContext(Dispatchers.IO) {
        try {
            // 重新注册以获取最新状态
            val result = register()
            result.map { it.status }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    fun clearAuth() {
        prefs.edit().clear().apply()
    }
}

data class RegisterResult(
    val status: String,
    val message: String,
    val token: String = ""
)
