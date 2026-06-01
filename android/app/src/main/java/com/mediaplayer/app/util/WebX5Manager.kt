package com.mediaplayer.app.util

import android.content.Context
import android.os.Build
import android.util.Log
import android.widget.Toast
import com.mediaplayer.app.data.api.ApiClient
import com.tencent.smtt.export.external.TbsCoreSettings
import com.tencent.smtt.sdk.QbSdk
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.net.URL

object WebX5Manager {
    var isInitialized = false
        private set
    var isX5CoreReady = false
        private set
    var downloadProgress = 0
        private set
    var isDownloading = false
        private set

    fun init(context: Context, callback: ((Boolean) -> Unit)? = null) {
        if (isInitialized) {
            callback?.invoke(true)
            return
        }

        // 配置 X5 内核参数
        val map = HashMap<String, Any>()
        map[TbsCoreSettings.TBS_SETTINGS_USE_SPEEDY_CLASSLOADER] = true
        map[TbsCoreSettings.TBS_SETTINGS_USE_DEXLOADER_SERVICE] = true
        QbSdk.initTbsSettings(map)

        // 判断是否已经能够加载内核（之前安装过）
        if (QbSdk.canLoadX5(context.applicationContext)) {
            Log.d("WebX5Manager", "X5 core already exists. Initializing...")
            initEnvironment(context.applicationContext, callback)
            return
        }

        // 需要下载
        if (isDownloading) return
        isDownloading = true
        downloadProgress = 0

        CoroutineScope(Dispatchers.IO).launch {
            try {
                val arch = Build.SUPPORTED_ABIS.firstOrNull() ?: "unknown"
                Log.d("WebX5Manager", "Requesting X5 core update for arch: $arch")
                
                // 请求服务端 API 获取下载链接
                val response = ApiClient.getService().checkX5Update(arch)
                if (response.isSuccessful) {
                    val body = response.body()
                    if (body != null && body.code == 0 && body.data != null) {
                        val update = body.data
                        downloadAndInstallCore(context.applicationContext, update.url, update.code, callback)
                    } else {
                        Log.e("WebX5Manager", "Failed to get X5 core update: ${body?.message}")
                        isDownloading = false
                        withContext(Dispatchers.Main) {
                            initEnvironment(context.applicationContext, callback) // 退回默认初始化
                        }
                    }
                } else {
                    Log.e("WebX5Manager", "Server returned error: ${response.code()}")
                    isDownloading = false
                    withContext(Dispatchers.Main) {
                        initEnvironment(context.applicationContext, callback)
                    }
                }
            } catch (e: Exception) {
                Log.e("WebX5Manager", "Network error while checking X5 update: ${e.message}")
                isDownloading = false
                withContext(Dispatchers.Main) {
                    initEnvironment(context.applicationContext, callback)
                }
            }
        }
    }

    private suspend fun downloadAndInstallCore(context: Context, downloadUrl: String, code: Int, callback: ((Boolean) -> Unit)?) {
        try {
            val apkName = "TBScore_$code.apk"
            val apkDir = context.filesDir.absolutePath
            val apkPath = "$apkDir/$apkName"
            val file = File(apkPath)

            if (!file.exists()) {
                Log.d("WebX5Manager", "Starting download: $downloadUrl")
                withContext(Dispatchers.Main) {
                    Toast.makeText(context, "正在从服务器下载X5内核...", Toast.LENGTH_SHORT).show()
                }

                val url = URL(downloadUrl)
                val connection = url.openConnection()
                connection.connect()
                
                val fileLength = connection.contentLength
                val inputStream = connection.getInputStream()
                val outputStream = file.outputStream()

                var downloaded = 0L
                val data = ByteArray(8192)
                var count: Int
                
                inputStream.use { input ->
                    outputStream.use { output ->
                        while (input.read(data).also { count = it } != -1) {
                            output.write(data, 0, count)
                            downloaded += count
                            if (fileLength > 0) {
                                val progress = (downloaded * 100 / fileLength).toInt()
                                if (progress / 10 > downloadProgress / 10) { // report every 10%
                                    downloadProgress = progress
                                    Log.d("WebX5Manager", "Download progress: $progress%")
                                }
                            }
                        }
                    }
                }
            }

            Log.d("WebX5Manager", "Download complete or file exists. Installing local core...")
            withContext(Dispatchers.Main) {
                Toast.makeText(context, "X5内核就绪，正在安装加载...", Toast.LENGTH_SHORT).show()
            }

            QbSdk.reset(context)
            QbSdk.installLocalTbsCore(context, code, apkPath)
            
            withContext(Dispatchers.Main) {
                initEnvironment(context, callback)
                isDownloading = false
            }
        } catch (e: Exception) {
            Log.e("WebX5Manager", "Download/Install failed: ${e.message}")
            isDownloading = false
            withContext(Dispatchers.Main) {
                initEnvironment(context, callback)
            }
        }
    }

    private fun initEnvironment(context: Context, callback: ((Boolean) -> Unit)?) {
        QbSdk.initX5Environment(context, object : QbSdk.PreInitCallback {
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
