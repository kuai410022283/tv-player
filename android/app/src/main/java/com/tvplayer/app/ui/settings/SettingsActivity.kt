package com.tvplayer.app.ui.settings

import android.os.Bundle
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import com.tvplayer.app.Prefs
import com.tvplayer.app.R
import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.api.ClientAuthManager
import com.tvplayer.app.util.DeviceUtils

class SettingsActivity : AppCompatActivity() {

    private lateinit var authManager: ClientAuthManager

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 强制所有设备使用相同的设置页面布局
        setContentView(R.layout.activity_settings)

        authManager = ClientAuthManager(this)

        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val etServerUrl = findViewById<android.widget.EditText>(R.id.etServerUrl)
        val etNetworkCache = findViewById<android.widget.EditText>(R.id.etNetworkCache)
        
        etServerUrl.setText(prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL))
        etNetworkCache.setText(prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE).toString())

        // 返回按钮 (手机模式)
        findViewById<android.view.View>(R.id.btnBack)?.setOnClickListener { finish() }

        // 填充关于信息
        try {
            val pInfo = packageManager.getPackageInfo(packageName, 0)
            val vCode = androidx.core.content.pm.PackageInfoCompat.getLongVersionCode(pInfo)
            findViewById<android.widget.TextView>(R.id.tvAppVersion)?.text = "${pInfo.versionName} ($vCode)"
        } catch (_: Exception) {
            findViewById<android.widget.TextView>(R.id.tvAppVersion)?.text = "1.0.0"
        }
        findViewById<android.widget.TextView>(R.id.tvDeviceId)?.text = authManager.getDeviceId().take(16) + "..."
        findViewById<android.widget.TextView>(R.id.tvAuthStatusInfo)?.text = when (authManager.getStatus()) {
            "approved" -> "已授权"
            "pending" -> "等待审批"
            "rejected" -> "已拒绝"
            "banned" -> "已封禁"
            "expired" -> "已过期"
            else -> "未注册"
        }

        findViewById<android.view.View>(R.id.btnSave).setOnClickListener {
            val url = etServerUrl.text.toString().trim()
            if (url.isEmpty()) {
                Toast.makeText(this, "请输入服务器地址", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }

            val oldUrl = prefs.getString(Prefs.KEY_SERVER_URL, "")
            val cacheStr = etNetworkCache.text.toString().trim()
            val cacheValue = if (cacheStr.isNotEmpty()) cacheStr.toIntOrNull() ?: Prefs.DEFAULT_NETWORK_CACHE else Prefs.DEFAULT_NETWORK_CACHE
            
            if (url != oldUrl) {
                authManager.clearAuth()
                ApiClient.reset()
                com.tvplayer.app.ui.home.MainActivity.settingsChanged = true
            }

            prefs.edit()
                .putString(Prefs.KEY_SERVER_URL, url)
                .putInt(Prefs.KEY_NETWORK_CACHE, cacheValue)
                .apply()
                
            ApiClient.init(url)
            Toast.makeText(this, "设置已保存，重新启动应用生效", Toast.LENGTH_SHORT).show()
            finish()
        }

        findViewById<android.view.View>(R.id.btnCancel).setOnClickListener { finish() }
    }
}
