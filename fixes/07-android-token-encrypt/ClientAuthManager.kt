// ========================================
// ClientAuthManager.kt — 使用 EncryptedSharedPreferences 存储 Token
// ========================================

package com.tvplayer.app.data.api

import android.content.Context
import android.content.SharedPreferences
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.tvplayer.app.Prefs

/**
 * 客户端认证管理器
 * ★ 修改：使用 EncryptedSharedPreferences 加密存储 Token
 */
class ClientAuthManager(private val context: Context) {

    // ★ 使用 EncryptedSharedPreferences 替代普通 SharedPreferences
    private val securePrefs: SharedPreferences by lazy {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()

        EncryptedSharedPreferences.create(
            context,
            "${Prefs.FILE}_secure",
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
        )
    }

    // 兼容旧的普通 SharedPreferences（用于迁移）
    private val oldPrefs: SharedPreferences by lazy {
        context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
    }

    fun getToken(): String? {
        // 优先从加密存储读取
        var token = securePrefs.getString(KEY_TOKEN, null)

        // 如果加密存储没有，尝试从旧存储迁移
        if (token == null) {
            token = oldPrefs.getString(Prefs.KEY_ACCESS_TOKEN, null)
            if (token != null) {
                // 迁移到加密存储
                securePrefs.edit().putString(KEY_TOKEN, token).apply()
                // 清除旧存储中的 token
                oldPrefs.edit().remove(Prefs.KEY_ACCESS_TOKEN).apply()
            }
        }

        return token
    }

    fun saveToken(token: String) {
        securePrefs.edit().putString(KEY_TOKEN, token).apply()
    }

    fun clearToken() {
        securePrefs.edit().remove(KEY_TOKEN).apply()
        oldPrefs.edit().remove(Prefs.KEY_ACCESS_TOKEN).apply()
    }

    fun getClientId(): Long {
        return securePrefs.getLong(KEY_CLIENT_ID, 0)
    }

    fun saveClientId(id: Long) {
        securePrefs.edit().putLong(KEY_CLIENT_ID, id).apply()
    }

    fun isLoggedIn(): Boolean {
        return getToken() != null
    }

    companion object {
        private const val KEY_TOKEN = "client_access_token"
        private const val KEY_CLIENT_ID = "client_id"
    }
}
