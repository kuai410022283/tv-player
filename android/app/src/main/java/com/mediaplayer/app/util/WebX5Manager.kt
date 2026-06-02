package com.mediaplayer.app.util

import android.content.Context
import android.util.Log
import com.tencent.smtt.export.external.TbsCoreSettings
import com.tencent.smtt.sdk.QbSdk
import com.tencent.smtt.sdk.TbsListener

object WebX5Manager {
    var isInitialized = false
        private set
    var isX5CoreReady = false
        private set
    var downloadProgress = 0
        private set

    fun init(context: Context, callback: ((Boolean) -> Unit)? = null) {
        if (isInitialized) {
            callback?.invoke(true)
            return
        }

        // 允许非 WiFi 环境下下载内核
        QbSdk.setDownloadWithoutWifi(true)

        // 配置 X5 内核参数
        val map = HashMap<String, Any>()
        map[TbsCoreSettings.TBS_SETTINGS_USE_SPEEDY_CLASSLOADER] = true
        map[TbsCoreSettings.TBS_SETTINGS_USE_DEXLOADER_SERVICE] = true
        QbSdk.initTbsSettings(map)

        // 设置下载与安装监听
        QbSdk.setTbsListener(object : TbsListener {
            override fun onDownloadFinish(errCode: Int) {
                Log.d("WebX5Manager", "onDownloadFinish: $errCode")
            }

            override fun onInstallFinish(errCode: Int) {
                Log.d("WebX5Manager", "onInstallFinish: $errCode")
            }

            override fun onDownloadProgress(progress: Int) {
                Log.d("WebX5Manager", "onDownloadProgress: $progress")
                downloadProgress = progress
            }
        })

        // 初始化
        QbSdk.initX5Environment(context.applicationContext, object : QbSdk.PreInitCallback {
            override fun onCoreInitFinished() {
                Log.d("WebX5Manager", "onCoreInitFinished")
            }

            override fun onViewInitFinished(isX5Core: Boolean) {
                Log.d("WebX5Manager", "onViewInitFinished, isX5Core=$isX5Core")
                isInitialized = true
                isX5CoreReady = isX5Core
                callback?.invoke(isX5Core)
            }
        })
    }
}
