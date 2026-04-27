package com.tvplayer.app.ui.settings

import android.os.Bundle
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import com.tvplayer.app.R
import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.api.ClientAuthManager
import com.tvplayer.app.util.DeviceUtils

class SettingsActivity : AppCompatActivity() {

    private lateinit var authManager: ClientAuthManager

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        val isTv = DeviceUtils.isTV(this)
        if (isTv) {
            setContentView(R.layout.activity_settings)
        } else {
            setContentView(R.layout.activity_settings_phone)
        }

        authManager = ClientAuthManager(this)

        val prefs = getSharedPreferences("tvplayer", MODE_PRIVATE)
        val etServerUrl = findViewById<android.widget.EditText>(R.id.etServerUrl)
        etServerUrl.setText(prefs.getString("server_url", "http://10.0.2.2:9527"))

        // 返回按钮 (手机模式)
        findViewById<android.view.View>(R.id.btnBack)?.setOnClickListener { finish() }

        findViewById<android.view.View>(R.id.btnSave).setOnClickListener {
            val url = etServerUrl.text.toString().trim()
            if (url.isEmpty()) {
                Toast.makeText(this, "请输入服务器地址", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }

            val oldUrl = prefs.getString("server_url", "")
            if (url != oldUrl) {
                authManager.clearAuth()
                ApiClient.reset()
            }

            prefs.edit().putString("server_url", url).apply()
            ApiClient.init(url)
            Toast.makeText(this, "设置已保存，重新启动应用生效", Toast.LENGTH_SHORT).show()
            finish()
        }

        findViewById<android.view.View>(R.id.btnCancel).setOnClickListener { finish() }
    }
}
