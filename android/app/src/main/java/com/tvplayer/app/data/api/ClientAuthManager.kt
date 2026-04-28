package com.tvplayer.app.data.api

import android.annotation.SuppressLint
import android.content.Context
import android.os.Build
import android.provider.Settings
import com.tvplayer.app.Prefs
import com.tvplayer.app.data.model.ClientRegisterResp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.util.UUID

/**
 * 客户端认证管理器 —— 处理设备注册、token 持久化、状态轮询。
 */
class ClientAuthManager(private val context: Context) {

    private val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)

    fun getDeviceId(): String {
        var id = prefs.getString(Prefs.KEY_DEVICE_ID, null)
        if (id.isNullOrEmpty()) {
            @SuppressLint("HardwareIds")
            val androidId = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
            id = androidId ?: UUID.randomUUID().toString()
            prefs.edit().putString(Prefs.KEY_DEVICE_ID, id).apply()
        }
        return id
    }

    fun getToken(): String? = prefs.getString(Prefs.KEY_ACCESS_TOKEN, null)

    fun getStatus(): String? = prefs.getString(Prefs.KEY_CLIENT_STATUS, null)

    fun getClientId(): Long {
        return prefs.getLong(Prefs.KEY_CLIENT_ID, 0)
    }

    fun isApproved(): Boolean {
        return getStatus() == "approved" && getToken() != null
    }

    /** 注册设备到后端 */
    suspend fun register(): Result<ClientRegisterResp> = withContext(Dispatchers.IO) {
        try {
            val body = mapOf(
                "name" to "${Build.MANUFACTURER} ${Build.MODEL}",
                "device_id" to getDeviceId(),
                "device_model" to Build.MODEL,
                "device_os" to "Android ${Build.VERSION.RELEASE}",
                "app_version" to "1.0.0"
            )
            val response = ApiClient.getService().clientRegister(body)
            if (response.isSuccessful) {
                val resp = response.body()!!
                val data = resp.data
                if (data != null) {
                    saveAuth(data)
                    Result.success(data)
                } else {
                    Result.failure(Exception(resp.message))
                }
            } else {
                Result.failure(Exception("注册失败: ${response.code()}"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /** 验证当前 token 是否有效 */
    suspend fun verify(): Result<Boolean> = withContext(Dispatchers.IO) {
        try {
            val token = getToken() ?: return@withContext Result.failure(Exception("无令牌"))
            val response = ApiClient.getService().clientVerify("Bearer $token")
            if (response.isSuccessful && response.body()?.code == 0) {
                Result.success(true)
            } else {
                Result.success(false)
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /** 轮询检查审批状态 */
    suspend fun checkStatus(): Result<String> = withContext(Dispatchers.IO) {
        try {
            val token = getToken() ?: return@withContext Result.failure(Exception("无令牌"))
            val response = ApiClient.getService().clientVerify("Bearer $token")
            if (response.isSuccessful) {
                val body = response.body()
                if (body?.code == 0) {
                    // token 有效 = 已审批
                    prefs.edit().putString(Prefs.KEY_CLIENT_STATUS, "approved").apply()
                    Result.success("approved")
                } else {
                    // 检查注册状态
                    val regResp = ApiClient.getService().clientRegister(
                        mapOf("name" to "", "device_id" to getDeviceId())
                    )
                    if (regResp.isSuccessful) {
                        val status = regResp.body()?.data?.status ?: "pending"
                        prefs.edit().putString(Prefs.KEY_CLIENT_STATUS, status).apply()
                        Result.success(status)
                    } else {
                        Result.success(prefs.getString(Prefs.KEY_CLIENT_STATUS, "pending") ?: "pending")
                    }
                }
            } else {
                Result.success(prefs.getString(Prefs.KEY_CLIENT_STATUS, "pending") ?: "pending")
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    private fun saveAuth(resp: ClientRegisterResp) {
        prefs.edit().apply {
            resp.accessToken.takeIf { it.isNotEmpty() }?.let {
                putString(Prefs.KEY_ACCESS_TOKEN, it)
                ApiClient.accessToken = it
            }
            putLong(Prefs.KEY_CLIENT_ID, resp.clientId)
            putString(Prefs.KEY_CLIENT_STATUS, resp.status)
            apply()
        }
        // 同步到 ApiClient
        resp.accessToken.takeIf { it.isNotEmpty() }?.let {
            ApiClient.accessToken = it
        }
    }

    fun clearAuth() {
        prefs.edit().apply {
            remove(Prefs.KEY_ACCESS_TOKEN)
            remove(Prefs.KEY_CLIENT_ID)
            remove(Prefs.KEY_CLIENT_STATUS)
            apply()
        }
        ApiClient.accessToken = null
    }
}
