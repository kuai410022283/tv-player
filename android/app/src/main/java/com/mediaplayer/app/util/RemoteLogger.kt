package com.mediaplayer.app.util

import android.content.Context
import android.util.Log
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.data.api.ApiClient
import kotlinx.coroutines.*
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.RequestBody.Companion.asRequestBody
import java.io.File
import java.text.SimpleDateFormat
import java.util.*
import java.util.concurrent.atomic.AtomicBoolean

object RemoteLogger {
    private const val TAG = "RemoteLogger"
    private const val MAX_FILE_SIZE = 1 * 1024 * 1024 // 1MB 切片
    private val scope = CoroutineScope(Dispatchers.IO + SupervisorJob())
    private var isRunning = AtomicBoolean(false)
    private var isEnabled = false

    private lateinit var logDir: File
    private var currentLogFile: File? = null
    private val dateFormat = SimpleDateFormat("yyyy-MM-dd HH:mm:ss.SSS", Locale.US)

    fun init(context: Context) {
        logDir = File(context.filesDir, "remote_logs")
        if (!logDir.exists()) {
            logDir.mkdirs()
        }
        
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        isEnabled = prefs.getBoolean(Prefs.KEY_ENABLE_LOG, false)

        if (isEnabled) {
            startUploadTask()
        }
    }

    fun updateConfig(enabled: Boolean) {
        isEnabled = enabled
        if (enabled && !isRunning.get()) {
            startUploadTask()
        }
    }

    fun log(level: String, tag: String, message: String, throwable: Throwable? = null) {
        if (!isEnabled) return

        scope.launch {
            try {
                val time = dateFormat.format(Date())
                val errorMsg = throwable?.stackTraceToString()?.let { "\n$it" } ?: ""
                val logLine = "[$time] [$level] [$tag] $message$errorMsg\n"

                val file = getCurrentLogFile()
                file.appendText(logLine)
                
                if (file.length() > MAX_FILE_SIZE) {
                    currentLogFile = null 
                }
            } catch (e: Exception) {
                Log.e(TAG, "写入日志失败: ${e.message}")
            }
        }
    }

    fun e(tag: String, message: String, throwable: Throwable? = null) {
        Log.e(tag, message, throwable)
        log("E", tag, message, throwable)
    }

    fun i(tag: String, message: String) {
        Log.i(tag, message)
        log("I", tag, message)
    }

    fun d(tag: String, message: String) {
        Log.d(tag, message)
        log("D", tag, message)
    }

    private fun getCurrentLogFile(): File {
        var file = currentLogFile
        if (file == null || !file.exists()) {
            val ts = System.currentTimeMillis()
            file = File(logDir, "log_$ts.txt")
            currentLogFile = file
        }
        return file
    }

    private fun startUploadTask() {
        if (isRunning.compareAndSet(false, true)) {
            scope.launch {
                while (isActive) {
                    if (!isEnabled) {
                        isRunning.set(false)
                        break
                    }

                    uploadPendingLogs()
                    delay(30_000) 
                }
            }
        }
    }

    private suspend fun uploadPendingLogs() {
        try {
            val current = currentLogFile
            if (current != null && current.length() > 0) {
                currentLogFile = null
            }

            val files = logDir.listFiles { _, name -> name.startsWith("log_") && name.endsWith(".txt") }
            if (files.isNullOrEmpty()) return

            val sortedFiles = files.sortedBy { it.name }
            for (file in sortedFiles) {
                if (file == currentLogFile) {
                    continue
                }

                val reqFile = file.asRequestBody("text/plain".toMediaTypeOrNull())
                val body = MultipartBody.Part.createFormData("log_file", file.name, reqFile)

                try {
                    val response = ApiClient.getService().clientUploadLog(body)
                    if (response.isSuccessful && response.body()?.code == 0) {
                        file.delete()
                    } else {
                        break 
                    }
                } catch (e: Exception) {
                    break
                }
            }
        } catch (e: Exception) {
            Log.e(TAG, "上传日志异常", e)
        }
    }
}
