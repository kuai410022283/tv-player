package com.mediaplayer.app.ui.player

import android.content.Context
import android.content.Intent
import android.media.AudioManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.KeyEvent
import android.view.View
import android.view.WindowManager
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.lifecycleScope
import android.net.Uri
import android.app.PictureInPictureParams
import android.util.Rational
import android.content.res.Configuration
import android.content.IntentFilter
import android.widget.ImageView
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.data.api.ApiClient
import com.mediaplayer.app.data.api.ClientAuthManager
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelLine
import com.mediaplayer.app.data.repository.ChannelRepository
import com.mediaplayer.app.service.PlaybackService
import com.mediaplayer.app.util.DeviceUtils
import com.mediaplayer.app.util.PlayerGestureController
import com.mediaplayer.app.MediaPlayerApp
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min

@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class PlayerActivity : AppCompatActivity(), com.mediaplayer.app.util.PipActionCallback {

    override fun attachBaseContext(newBase: Context) {
        val prefs = newBase.getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val langInt = prefs.getInt(Prefs.KEY_APP_LANGUAGE, Prefs.LANG_AUTO)
        val langCode = when (langInt) {
            Prefs.LANG_ZH_CN -> "zh-CN"
            Prefs.LANG_EN -> "en"
            Prefs.LANG_ZH_TW -> "zh-TW"
            Prefs.LANG_KO -> "ko"
            Prefs.LANG_JA -> "ja"
            else -> "auto"
        }
        super.attachBaseContext(com.mediaplayer.app.util.LocaleHelper.wrap(newBase, langCode))
    }

    override fun getResources(): android.content.res.Resources {
        val res = super.getResources()
        val dm = res.displayMetrics
        if (dm.widthPixels > 0 && dm.heightPixels > 0) {
            val shortSide = Math.min(dm.widthPixels, dm.heightPixels)
            val targetDensity = shortSide / 720f
            if (Math.abs(dm.density - targetDensity) > 0.01f) {
                val targetScaledDensity = targetDensity * (dm.scaledDensity / dm.density)
                val targetDensityDpi = (160 * targetDensity).toInt()
                dm.density = targetDensity
                dm.scaledDensity = targetScaledDensity
                dm.densityDpi = targetDensityDpi
            }
        }
        return res
    }

    private var playerHelper: com.mediaplayer.app.util.IPlayerHelper? = null
    private val repo = ChannelRepository()
    private lateinit var authManager: ClientAuthManager
    private var isTvMode = false

    // ── 播放重试控制 ──
    private var retryCount = 0
    private val maxRetries = 4

    // ── Views ──
    private var videoLayout: android.widget.FrameLayout? = null
    private var progressBar: View? = null
    private var layoutChannelInfo: View? = null
    private var tvChannelName: android.widget.TextView? = null
    private var tvStreamType: android.widget.TextView? = null
    private var tvStatus: android.widget.TextView? = null
    private var tvResolution: android.widget.TextView? = null

    // ── EPG views ──
    private var layoutEpg: View? = null
    private var tvEpgNow: android.widget.TextView? = null
    private var tvEpgNext: android.widget.TextView? = null

    // ── OSD UI elements ──
    private var layoutRemoteHint: View? = null

    // ── Phone-only views ──
    private var layoutGestureHint: View? = null
    private var layoutVolumeIndicator: View? = null
    private var progressVolume: android.widget.ProgressBar? = null
    private var tvVolume: android.widget.TextView? = null
    private var layoutBrightnessIndicator: View? = null
    private var progressBrightness: android.widget.ProgressBar? = null
    private var tvSpeedIndicator: android.widget.TextView? = null

    // ── Gesture ──
    private var gestureController: PlayerGestureController? = null
    private var audioManager: AudioManager? = null
    private var maxVolume = 15
    private var isLongPressingSpeed = false
    private var gestureHintShown = false

    // ── Data ──
    private var channelId = 0L
    private var channelName = ""
    private var streamUrl = ""
    private var streamType = "hls"
    private var channelIndex = 0
    private var proxyType = ""
    private var proxyUrl = ""
    private var lineIndex = 0
    private var allChannels = listOf<Channel>()
    private var resolveJob: kotlinx.coroutines.Job? = null
    private var isPipEnabled = false
    private var isPipClosedBySystem = false

    private val handler = Handler(Looper.getMainLooper())
    private val hideInfoRunnable = Runnable { hideChannelInfo() }
    private val hideVolumeRunnable = Runnable { layoutVolumeIndicator?.visibility = View.GONE }
    private val hideBrightnessRunnable = Runnable { layoutBrightnessIndicator?.visibility = View.GONE }
    private val hideSpeedRunnable = Runnable { tvSpeedIndicator?.visibility = View.GONE }

    // PiP Controller
    private lateinit var pipController: com.mediaplayer.app.util.PipController

    // ── Retry ──
    private var continuousSkipCount = 0
    private val maxAutoSkips = 5
    private var coreRetryLevel = 0 // 0=默认, 1=MPV
    private var backPressedTime = 0L
    private var isWatchdogEnabledForCurrentStream = false

    // ── Watchdog ──
    enum class PlaybackState { IDLE, BUFFERING, PLAYING }
    private var currentPlaybackState = PlaybackState.IDLE
    private var stateStartTime = 0L
    private var lastPlaybackTime = 0L
    private var frozenTimeCounter = 0
    private var heartbeatRunnable: Runnable? = null

    private val watchdogRunnable = object : Runnable {
        override fun run() {
            if (playerHelper != null) {
                val now = System.currentTimeMillis()
                when (currentPlaybackState) {
                    PlaybackState.BUFFERING -> {
                        // 动态看门狗超时：默认 10秒；直播/特殊流放宽到 25秒，给足大水库蓄水时间
                        val timeoutLimit = if (!isWatchdogEnabledForCurrentStream) 25000L else 10000L
                        if (stateStartTime > 0 && now - stateStartTime > timeoutLimit) {
                            tvStatus?.text = getString(R.string.player_status_timeout)
                            currentPlaybackState = PlaybackState.IDLE
                            handlePlaybackError(isNetworkTimeout = true)
                            return
                        }
                    }
                    PlaybackState.PLAYING -> {
                        if (!isWatchdogEnabledForCurrentStream) {
                            lastPlaybackTime = playerHelper?.getTime() ?: 0L
                            frozenTimeCounter = 0
                        } else {
                            val currentTime = playerHelper?.getTime() ?: 0L
                            if (currentTime > 0 && currentTime == lastPlaybackTime) {
                                frozenTimeCounter++
                                if (frozenTimeCounter >= 4) { // 4 * 2s = 8s
                                    tvStatus?.text = getString(R.string.player_status_stuck)
                                    currentPlaybackState = PlaybackState.IDLE
                                    handlePlaybackError()
                                    frozenTimeCounter = 0
                                    return
                                }
                            } else {
                                lastPlaybackTime = currentTime
                                frozenTimeCounter = 0
                            }
                        }
                    }
                    PlaybackState.IDLE -> {
                        frozenTimeCounter = 0
                    }
                }
            }
            handler.postDelayed(this, 2000)
        }
    }

    override fun onConfigurationChanged(newConfig: android.content.res.Configuration) {
        super.onConfigurationChanged(newConfig)
        findViewById<android.view.View>(R.id.videoLayout)?.requestLayout()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 安全地请求横屏方向，规避 Android 8.0 透明主题请求固定方向时的崩溃Bug
        try {
            requestedOrientation = android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        } catch (e: Exception) {
            // 忽略 Android 8.0 上的 IllegalStateException
        }
        
        // 兼容刘海屏/挖孔屏/灵动岛：允许画面延伸到全部屏幕边缘
        // Android 15+ (API 35): ALWAYS 模式确保横屏时灵动岛/长边缺口区域也被覆盖
        // Android 9-14 (API 28-34): SHORT_EDGES 已足够覆盖所有刘海/挖孔场景
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            val lp = window.attributes
            lp.layoutInDisplayCutoutMode = if (Build.VERSION.SDK_INT >= 35) {
                // LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS = 3 (Android 15+)
                3
            } else {
                WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
            }
            window.attributes = lp
        }

        // 强制所有设备使用 TV 模式逻辑
        isTvMode = true
        
        pipController = com.mediaplayer.app.util.PipController(this, this)

        setContentView(R.layout.activity_player)
        setupViews()
        
        if (!isTvMode) {
            setupGestures()
            showGestureHintOnce()
        }

        hideSystemUI()
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        authManager = ClientAuthManager(this)
        audioManager = getSystemService(AUDIO_SERVICE) as AudioManager
        maxVolume = audioManager?.getStreamMaxVolume(AudioManager.STREAM_MUSIC) ?: 15

        channelId = intent.getLongExtra("channel_id", 0)
        channelName = intent.getStringExtra("channel_name") ?: ""
        streamUrl = intent.getStringExtra("stream_url") ?: ""
        streamType = intent.getStringExtra("stream_type") ?: "hls"
        channelIndex = intent.getIntExtra("channel_index", 0)
        proxyType = intent.getStringExtra("proxy_type") ?: ""
        proxyUrl = intent.getStringExtra("proxy_url") ?: ""
        val userAgent = intent.getStringExtra("user_agent") ?: ""
        val customHeaders = intent.getStringExtra("custom_headers") ?: ""

        playStream(streamUrl, streamType, userAgent, customHeaders)
        loadChannels()
        showChannelInfo()
        startHeartbeat()
    }

    override fun onResume() {
        super.onResume()
        // 停止保活服务
        stopService(Intent(this, PlaybackService::class.java))
        playerHelper?.resume()
        hideSystemUI()
        
        // 委托给 PipController 动态参数更新
        pipController.updatePipParams(playerHelper?.isPlaying() == true)
    }

    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        pipController.handleUserLeaveHint(playerHelper?.isPlaying() == true)
    }

    override fun onPictureInPictureModeChanged(isInPictureInPictureMode: Boolean, newConfig: Configuration) {
        super.onPictureInPictureModeChanged(isInPictureInPictureMode, newConfig)
        
        val viewsToHide = listOf(
            layoutChannelInfo,
            progressBar,
            layoutRemoteHint,
            layoutGestureHint,
            layoutVolumeIndicator,
            layoutBrightnessIndicator,
            tvSpeedIndicator
        )
        pipController.handlePictureInPictureModeChanged(isInPictureInPictureMode, viewsToHide)
        
        if (isInPictureInPictureMode) {
            handler.removeCallbacks(hideInfoRunnable)
        } else {
            if (lifecycle.currentState != androidx.lifecycle.Lifecycle.State.RESUMED) {
                isPipClosedBySystem = true
            }
            
            // 退出画中画：恢复默认全屏UI
            if (playerHelper?.isPlaying() == false && currentPlaybackState == PlaybackState.IDLE) {
                // 如果画中画期间播放结束或失败，退出时关闭 Activity
                finish()
            } else {
                // 恢复底部操作按键提示
                layoutRemoteHint?.visibility = View.VISIBLE
                // 恢复顶部频道信息，并触发倒计时隐藏
                showChannelInfo()
            }
        }
    }

    // ═══════════════════════════════════════════════════
    // VIEW SETUP
    // ═══════════════════════════════════════════════════

    private fun setupViews() {
        videoLayout = findViewById(R.id.videoLayout)
        progressBar = findViewById(R.id.progressBar)
        layoutChannelInfo = findViewById(R.id.layoutChannelInfo)
        tvChannelName = findViewById(R.id.tvChannelName)
        tvStreamType = findViewById(R.id.tvStreamType)
        tvStatus = findViewById(R.id.tvStatus)
        tvResolution = findViewById(R.id.tvResolution)
        layoutEpg = findViewById(R.id.layoutEpg)
        tvEpgNow = findViewById(R.id.tvEpgNow)
        tvEpgNext = findViewById(R.id.tvEpgNext)
        layoutRemoteHint = findViewById(R.id.layoutRemoteHint)

        val btnCast = findViewById<ImageView>(R.id.btnCast)
        btnCast?.setOnClickListener {
            val currentChannel = if (channelIndex in allChannels.indices) allChannels[channelIndex].id else -1L
            val currentPosition = playerHelper?.getTime() ?: 0L
            com.mediaplayer.app.ui.widget.CastDialog(this, currentChannel, currentPosition).show()
        }

        layoutGestureHint = findViewById(R.id.layoutGestureHint)
        layoutVolumeIndicator = findViewById(R.id.layoutVolumeIndicator)
        progressVolume = findViewById(R.id.progressVolume)
        tvVolume = findViewById(R.id.tvVolume)
        layoutBrightnessIndicator = findViewById(R.id.layoutBrightnessIndicator)
        progressBrightness = findViewById(R.id.progressBrightness)
        tvSpeedIndicator = findViewById(R.id.tvSpeedIndicator)
    }

    private fun setupGestures() {
        gestureController = PlayerGestureController(this, object : PlayerGestureController.GestureListener {
            override fun onChannelNext() = nextChannel()
            override fun onChannelPrev() = prevChannel()
            override fun onToggleInfo() = toggleChannelInfo()
            override fun onTogglePlayPause() {
                playerHelper?.let { if (it.isPlaying()) it.pause() else it.resume() }
            }

            override fun onVolumeChange(delta: Float) {
                val current = audioManager?.getStreamVolume(AudioManager.STREAM_MUSIC) ?: 0
                val newVol = max(0, min(maxVolume, current + (delta * maxVolume).toInt()))
                audioManager?.setStreamVolume(AudioManager.STREAM_MUSIC, newVol, 0)
                showVolumeIndicator(newVol)
            }

            override fun onBrightnessChange(delta: Float) {
                val lp = window.attributes
                val current = if (lp.screenBrightness < 0) 0.5f else lp.screenBrightness
                val newBrightness = max(0.01f, min(1.0f, current + delta * 0.3f))
                lp.screenBrightness = newBrightness
                window.attributes = lp
                showBrightnessIndicator((newBrightness * 100).toInt())
            }

            override fun onSeekDelta(deltaMs: Long) {
                playerHelper?.let { p ->
                    val newPos = max(0, p.getTime() + deltaMs)
                    p.setTime(newPos)
                }
            }

            override fun onLongPressStart() {
                isLongPressingSpeed = true
                playerHelper?.setRate(2.0f)
                showSpeedIndicator("2.0x ▶▶")
            }

            override fun onLongPressEnd() {
                isLongPressingSpeed = false
                playerHelper?.setRate(1.0f)
                showSpeedIndicator("1.0x ▶")
            }
        })

        // 绑定到 VideoLayout 的覆盖层
        val gestureOverlay = findViewById<View>(R.id.videoLayout)
        gestureController?.attachTo(gestureOverlay)
    }

    private fun showGestureHintOnce() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        if (!prefs.getBoolean(Prefs.KEY_GESTURE_HINT_SHOWN, false)) {
            layoutGestureHint?.visibility = View.VISIBLE
            layoutGestureHint?.setOnClickListener {
                layoutGestureHint?.visibility = View.GONE
                prefs.edit().putBoolean(Prefs.KEY_GESTURE_HINT_SHOWN, true).apply()
            }
            handler.postDelayed({
                layoutGestureHint?.visibility = View.GONE
                prefs.edit().putBoolean(Prefs.KEY_GESTURE_HINT_SHOWN, true).apply()
            }, 5000)
        }
    }

    // ═══════════════════════════════════════════════════
    // PLAYER
    // ═══════════════════════════════════════════════════

    private fun hideSystemUI() {
        WindowCompat.setDecorFitsSystemWindows(window, false)
        WindowInsetsControllerCompat(window, window.decorView).let { controller ->
            controller.hide(WindowInsetsCompat.Type.systemBars())
            controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }
    }

    private fun initPlayerWithCore(core: Int) {
        val listener = object : com.mediaplayer.app.util.IPlayerHelper.PlayerListener {
            override fun onBuffering(percent: Float) {
                runOnUiThread {
                    if (currentPlaybackState != PlaybackState.BUFFERING) {
                        currentPlaybackState = PlaybackState.BUFFERING
                        stateStartTime = System.currentTimeMillis()
                    }
                    if (percent == 100f) {
                        progressBar?.visibility = View.GONE
                        tvStatus?.text = getString(R.string.player_status_playing)
                        handler.postDelayed({ hideChannelInfo() }, 3000)
                    } else {
                        progressBar?.visibility = View.VISIBLE
                        tvStatus?.text = getString(R.string.player_status_buffering, percent.toInt().toString())
                    }
                }
            }

            override fun onPlaying(resolution: String) {
                runOnUiThread {
                    currentPlaybackState = PlaybackState.PLAYING
                    stateStartTime = System.currentTimeMillis()
                    progressBar?.visibility = View.GONE
                    tvStatus?.text = getString(R.string.player_status_playing)
                    continuousSkipCount = 0
                    retryCount = 0
                    pipController.updatePipParams(true)
                    
                    lifecycleScope.launch {
                        val isProxy = streamUrl.contains("/stream/proxy/") == true
                        com.mediaplayer.app.util.PlaybackStateReporter.reportPlaying(channelId, isProxy, playerHelper?.getBandwidth() ?: 0L, streamUrl)
                    }

                    if (resolution.isNotEmpty()) {
                        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
                        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
                        val decoderStr = when (decoderMode) {
                            Prefs.DECODER_MODE_HARDWARE -> "HW"
                            Prefs.DECODER_MODE_SOFTWARE -> "SW"
                            else -> "Auto"
                        }
                        val coreStr = tvStreamType?.text?.toString() ?: ""
                        
                        val fullInfo = buildString {
                            if (resolution.isNotEmpty()) append(resolution)
                            if (decoderStr.isNotEmpty()) {
                                if (isNotEmpty()) append(" | ")
                                append(decoderStr)
                            }
                            if (coreStr.isNotEmpty()) {
                                if (isNotEmpty()) append(" | ")
                                append(coreStr)
                            }
                        }
                        tvResolution?.text = fullInfo
                    }
                }
            }

            override fun onError() {
                handler.post { 
                    currentPlaybackState = PlaybackState.IDLE
                    pipController.updatePipParams(false)
                    handlePlaybackError(isNetworkTimeout = false) 
                }
            }
            override fun onPlaybackCompleted() {
                handler.post {
                    currentPlaybackState = PlaybackState.IDLE
                    pipController.updatePipParams(false)
                    handlePlaybackCompleted()
                }
            }
            override fun onMediaInfoReady(badgeInfo: com.mediaplayer.app.util.StreamBadgeInfo) {
                // 面向发烧友/PT玩家：在此丢弃通俗的中文标签，保留 onPlaying 时最初提取的底层原始媒体流参数。
            }
        }
        
        when (core) {
            Prefs.PLAYER_CORE_EXO -> {
                playerHelper = com.mediaplayer.app.util.ExoPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
            }
            Prefs.PLAYER_CORE_MPV -> {
                playerHelper = com.mediaplayer.app.util.MpvPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
            }
            else -> {
                playerHelper = com.mediaplayer.app.util.ExoPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
            }
        }
    }

    private fun playCurrentLine() {
        val channel = allChannels.getOrNull(channelIndex) ?: return
        val lines = channel.getLinesSafely()
        if (lines.isEmpty()) return

        if (lineIndex >= lines.size) lineIndex = 0
        
        val line = lines[lineIndex]
        streamUrl = line.streamUrl
        streamType = line.streamType

        playStream(streamUrl, streamType, line.userAgent, line.customHeaders)
    }

    private fun playStream(url: String, type: String, userAgent: String = "", customHeaders: String = "") {
        progressBar?.visibility = View.VISIBLE
        tvChannelName?.text = channelName
        
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        var globalCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        if (globalCore !in 0..3) {
            globalCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, globalCore).apply()
        }
        
        var desiredCore = globalCore
        var coreText = ""
        
        if (globalCore == Prefs.PLAYER_CORE_AUTO) {
            if (coreRetryLevel > 0) {
                desiredCore = when (coreRetryLevel) {
                    1 -> { coreText = "容灾 (MPV)"; Prefs.PLAYER_CORE_MPV }
                    else -> desiredCore
                }
            } else {
                // 智能模式内核选择策略：
                // - RTP/UDP/TS：始终优先 ExoPlayer（直连时经 Go 代理转 HTTP，关代理时会失败后容灾 MPV）
                // - RTSP：代理开启时走 ExoPlayer（Go 代理把 rtsp→HTTP，Exo 更稳定）；
                //         代理关闭时走 MPV（MPV 原生 RTSP 兼容性更好）
                val proxyEnabled = MediaPlayerApp.isProxyEnabled && MediaPlayerApp.localProxyPort > 0
                desiredCore = when (type.lowercase()) {
                    "ts", "rtp", "udp" -> {
                        coreText = "智能 (Exo)"
                        Prefs.PLAYER_CORE_EXO
                    }
                    "rtsp" -> {
                        if (proxyEnabled) {
                            coreText = "智能 (Exo+代理)"
                            Prefs.PLAYER_CORE_EXO
                        } else {
                            coreText = "智能 (MPV)"
                            Prefs.PLAYER_CORE_MPV
                        }
                    }
                    else -> {
                        coreText = "智能 (Exo)"
                        Prefs.PLAYER_CORE_EXO
                    }
                }
            }
        } else {
            coreText = when (desiredCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                Prefs.PLAYER_CORE_MPV -> "MPV"
                else -> "Auto"
            }
        }
        val displayType = if (type.isEmpty()) "AUTO" else type.uppercase()
        tvStreamType?.text = "$displayType ($coreText)"
        
        val isCoreMatch = when (desiredCore) {
            Prefs.PLAYER_CORE_EXO -> playerHelper is com.mediaplayer.app.util.ExoPlayerHelper
            Prefs.PLAYER_CORE_MPV -> playerHelper is com.mediaplayer.app.util.MpvPlayerHelper
            else -> playerHelper is com.mediaplayer.app.util.ExoPlayerHelper
        }

        var needsRebuildDelay = false
        if (playerHelper == null || !isCoreMatch) {
            needsRebuildDelay = true
            playerHelper?.release()
            videoLayout?.removeAllViews() // 清除旧的视图
            initPlayerWithCore(desiredCore)
        }

        // 优先读取该频道的独立解码记忆（自愈自适应结果）；若无记忆，回退全局设置
        val currentChannel = allChannels.getOrNull(channelIndex)
        val channelVideoDecode = if (currentChannel != null) {
            prefs.getInt(Prefs.getKeyChannelVideoDecode(currentChannel.id), -1)
        } else -1

        val effectiveDecoderMode = if (channelVideoDecode != -1) {
            channelVideoDecode
        } else {
            prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        }
        playerHelper?.setDecoderMode(effectiveDecoderMode)

        // 应用保存的画面比例设置
        val savedScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        playerHelper?.setAspectRatio(savedScaleMode)

        resolveJob?.cancel()
        resolveJob = lifecycleScope.launch {
            // 解决主备切换等场景下，过快重建播放器导致硬件解码器耗尽/死锁的问题
            if (needsRebuildDelay) {
                kotlinx.coroutines.delay(200)
            }
            val finalUrl = com.mediaplayer.app.util.StreamResolver.resolve(url, userAgent, customHeaders)
            
            val lowerUrl = finalUrl.lowercase()
            val streamTypeLower = type.lowercase()
            val isMulticastOrLive = lowerUrl.startsWith("udp://") || 
                                    lowerUrl.startsWith("rtp://") || 
                                    lowerUrl.contains(".ts") || 
                                    lowerUrl.contains(".flv") || 
                                    lowerUrl.contains(".m3u8") || 
                                    lowerUrl.contains("/stream/proxy/") || 
                                    streamTypeLower in listOf("ts", "rtp", "udp", "flv", "hls", "m3u8")
            // 对于组播、ts、flv等特殊流，放行看门狗（不检测假死）
            isWatchdogEnabledForCurrentStream = !isMulticastOrLive
            
            currentPlaybackState = PlaybackState.BUFFERING
            stateStartTime = System.currentTimeMillis()
            
            val inferredContentType = if (streamType in listOf("ts", "hls", "flv", "rtsp", "rtmp")) "live" else "vod"
            val proxyChannel = if (proxyType.isNotEmpty() || proxyUrl.isNotEmpty()) {
                Channel(proxyType = proxyType, proxyUrl = proxyUrl)
            } else null
            playerHelper?.play(finalUrl, userAgent, customHeaders, contentType = inferredContentType, streamType = streamType, channel = proxyChannel)
        }
        
        currentPlaybackState = PlaybackState.BUFFERING
        stateStartTime = System.currentTimeMillis()
        lastPlaybackTime = 0L
        frozenTimeCounter = 0
        handler.removeCallbacks(watchdogRunnable)
        handler.postDelayed(watchdogRunnable, 2000)
    }

    private fun startHeartbeat() {
        heartbeatRunnable?.let { handler.removeCallbacks(it) }
        val runnable = object : Runnable {
            override fun run() {
                lifecycleScope.launch {
                    try {
                        val sessionId = com.mediaplayer.app.util.PlaybackStateReporter.currentSessionId
                        val speedBytes = playerHelper?.getBandwidth() ?: 0L
                        authManager.verify(sessionId, speedBytes).onSuccess { resp ->
                            if (resp != null && resp.globalMaintenance && !resp.isTester) {
                                playerHelper?.release()
                                playerHelper = null
                                finish()
                            }
                        }.onFailure { e ->
                            if (e.message == "KICKED_TEMP") {
                                playerHelper?.release()
                                playerHelper = null
                                runOnUiThread {
                                    android.widget.Toast.makeText(this@PlayerActivity, getString(R.string.toast_player_kicked), android.widget.Toast.LENGTH_LONG).show()
                                }
                                finish()
                            }
                        }
                    } catch (e: Exception) {}
                }
                heartbeatRunnable?.let { handler.postDelayed(it, 10000) }
            }
        }
        heartbeatRunnable = runnable
        handler.postDelayed(runnable, 10000)
    }

    private fun handlePlaybackCompleted() {
        // 播放自然结束（仅 catchup 回看会触发，直播流不会）
        currentPlaybackState = PlaybackState.IDLE
        com.mediaplayer.app.util.RemoteLogger.i("Player", "Playback completed naturally.")
        tvStatus?.text = getString(R.string.player_status_ended)
        progressBar?.visibility = View.GONE
    }

    private fun handlePlaybackError(isNetworkTimeout: Boolean = false) {
        // 手动指定内核模式：不做任何重试或自动切换，仅提示用户
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val globalCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        if (globalCore != Prefs.PLAYER_CORE_AUTO) {
            val coreName = when (globalCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                Prefs.PLAYER_CORE_MPV -> "MPV"
                else -> "Auto"
            }
            currentPlaybackState = PlaybackState.IDLE
            progressBar?.visibility = View.GONE
            tvStatus?.text = getString(R.string.player_status_unsupported, coreName)
            com.mediaplayer.app.util.RemoteLogger.e("Player", "Manual core ($coreName) playback failed. No auto-switch in manual mode.")
            return
        }

        retryCount++
        if (retryCount > maxRetries) {
            currentPlaybackState = PlaybackState.IDLE
            progressBar?.visibility = View.GONE
            tvStatus?.text = getString(R.string.player_status_failed)
            continuousSkipCount = 0
            return
        }

        currentPlaybackState = PlaybackState.IDLE
        progressBar?.visibility = View.GONE

        if (retryCount > 1) {
            val delayMs = (1L shl retryCount) * 1000L
            tvStatus?.text = getString(R.string.player_status_retry_timer, (delayMs / 1000).toString())
            handler.postDelayed({ executeRetry(isNetworkTimeout) }, delayMs)
            return
        }

        executeRetry(isNetworkTimeout)
    }

    private fun executeRetry(isNetworkTimeout: Boolean) {
        // 智能切换模式下的内核容灾
        if (coreRetryLevel < 1) {
            coreRetryLevel++

            // 直连 RTP/UDP 流判断：用原始 URL（代理化后 streamUrl 已变为 http://127.0.0.1:xxx/proxy?...）
            val originalLower = streamUrl.lowercase()
            val isDirectRtpOrUdp = originalLower.startsWith("udp://") || originalLower.startsWith("rtp://") ||
                originalLower.contains("proxy?url=udp") || originalLower.contains("proxy?url=rtp")
            val proxyEnabled = MediaPlayerApp.isProxyEnabled && MediaPlayerApp.localProxyPort > 0

            if (isDirectRtpOrUdp && proxyEnabled) {
                // 代理已开启时：RTP/UDP 经代理播放失败，MPV 无法通过代理URL直接获益，跳过容灾直接换线路
                coreRetryLevel = 0
            } else if (isDirectRtpOrUdp && !proxyEnabled) {
                // 代理未开启时：RTP/UDP 可以让 MPV 原生处理（MPV 支持 rtp:// udp://）
                val coreName = "MPV"
                tvStatus?.text = getString(R.string.player_status_retrying, coreName)
                playCurrentLine()
                return
            } else {
                val coreName = when (coreRetryLevel) {
                    1 -> "MPV"
                    else -> "ExoPlayer"
                }
                tvStatus?.text = getString(R.string.player_status_retrying, coreName)
                playCurrentLine()
                return
            }
        }

        coreRetryLevel = 0
        val channel = allChannels.getOrNull(channelIndex)
        val lines = channel?.getLinesSafely() ?: emptyList()

        if (lines.isNotEmpty() && lineIndex < lines.size - 1) {
            lineIndex++
            tvStatus?.text = getString(R.string.player_status_line_failed, (lineIndex + 1).toString())
            playCurrentLine()
        } else {
            coreRetryLevel = 0
            lineIndex = 0

            continuousSkipCount++
            if (continuousSkipCount >= maxAutoSkips) {
                tvStatus?.text = getString(R.string.player_status_all_failed)
                continuousSkipCount = 0
            } else {
                tvStatus?.text = getString(R.string.player_status_channel_skipped)
                if (allChannels.isNotEmpty()) {
                    val nextIdx = if (channelIndex < allChannels.size - 1) channelIndex + 1 else 0
                    handler.postDelayed({
                        switchChannel(nextIdx, isAutoSkip = true)
                    }, 1000)
                }
            }
        }
    }

    private fun loadChannels() {
        lifecycleScope.launch {
            val realGroups = repo.getGroups().getOrElse { emptyList() }
            repo.getAllChannelsByGroups(realGroups).onSuccess { allChannels = it }
        }
    }

    // ═══════════════════════════════════════════════════
    // CHANNEL SWITCHING
    // ═══════════════════════════════════════════════════

    private fun switchChannel(index: Int, isAutoSkip: Boolean = false) {
        if (!isAutoSkip) {
            continuousSkipCount = 0
            retryCount = 0
        }
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return

        // 切换频道前，保存上一个频道的观看时长
        if (channelId > 0) {
            saveProgress()
        }

        channelIndex = index
        lineIndex = 0 // 重置为第一条线路
        val channel = allChannels[index]
        channelId = channel.id
        channelName = channel.name

        playCurrentLine()
        showChannelInfo()
    }

    private fun nextChannel() {
        if (allChannels.isEmpty()) {
            Toast.makeText(this, getString(R.string.toast_player_loading_channels), Toast.LENGTH_SHORT).show()
            return
        }
        if (channelIndex < allChannels.size - 1) switchChannel(channelIndex + 1)
        else switchChannel(0) // 循环到第一个
    }

    private fun prevChannel() {
        if (allChannels.isEmpty()) {
            Toast.makeText(this, getString(R.string.toast_player_loading_channels), Toast.LENGTH_SHORT).show()
            return
        }
        if (channelIndex > 0) switchChannel(channelIndex - 1)
        else switchChannel(allChannels.size - 1) // 循环到最后一个
    }

    // ═══════════════════════════════════════════════════
    // UI OVERLAYS
    // ═══════════════════════════════════════════════════

    private fun showChannelInfo() {
        layoutChannelInfo?.visibility = View.VISIBLE
        tvChannelName?.isSelected = true
        loadEPG()
        handler.removeCallbacks(hideInfoRunnable)
        handler.postDelayed(hideInfoRunnable, 5000)
    }

    private fun loadEPG() {
        val channel = allChannels.getOrNull(channelIndex)
        if (channel != null && channel.currentEpg.isNotEmpty()) {
            layoutEpg?.visibility = View.VISIBLE
            tvEpgNow?.text = getString(R.string.player_epg_now, channel.currentEpg)
            tvEpgNow?.isSelected = true
            tvEpgNext?.text = ""
        } else {
            layoutEpg?.visibility = View.GONE
        }
    }

    private fun toggleChannelInfo() {
        if (layoutChannelInfo?.visibility == View.VISIBLE) hideChannelInfo() else showChannelInfo()
    }

    private fun hideChannelInfo() {
        layoutChannelInfo?.visibility = View.GONE
    }

    private fun showVolumeIndicator(vol: Int) {
        progressVolume?.progress = (vol * 100 / maxVolume)
        tvVolume?.text = getString(R.string.player_volume, vol.toString())
        layoutVolumeIndicator?.visibility = View.VISIBLE
        handler.removeCallbacks(hideVolumeRunnable)
        handler.postDelayed(hideVolumeRunnable, 1500)
    }

    private fun showBrightnessIndicator(pct: Int) {
        progressBrightness?.progress = pct
        layoutBrightnessIndicator?.visibility = View.VISIBLE
        handler.removeCallbacks(hideBrightnessRunnable)
        handler.postDelayed(hideBrightnessRunnable, 1500)
    }

    private fun showSpeedIndicator(text: String) {
        tvSpeedIndicator?.text = text
        tvSpeedIndicator?.visibility = View.VISIBLE
        if (!isLongPressingSpeed) {
            handler.removeCallbacks(hideSpeedRunnable)
            handler.postDelayed(hideSpeedRunnable, 2000)
        }
    }

    private fun saveProgress() {
        val lastPos = playerHelper?.getTime()?.div(1000)?.toInt() ?: 0
        var duration = 0
        if (stateStartTime > 0 && currentPlaybackState == PlaybackState.PLAYING) {
            duration = ((System.currentTimeMillis() - stateStartTime) / 1000).toInt()
        }
        val clientId = authManager.getClientId()
        
        // 记录播放历史
        val cId = channelId
        if (cId > 0 && duration > 0) {
            lifecycleScope.launch { repo.addHistory(cId, duration, lastPos, clientId) }
        }
    }

    // ═══════════════════════════════════════════════════
    // TV KEY EVENTS (D-pad)
    // ═══════════════════════════════════════════════════

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (!isTvMode) {
            // 手机模式只处理基本按键
            when (keyCode) {
                KeyEvent.KEYCODE_BACK -> {
                    if (backPressedTime + 2000 > System.currentTimeMillis()) {
                        saveProgress(); finish()
                    } else {
                        Toast.makeText(this, getString(R.string.toast_player_press_back_again), Toast.LENGTH_SHORT).show()
                        backPressedTime = System.currentTimeMillis()
                    }
                    return true
                }
                KeyEvent.KEYCODE_VOLUME_UP -> {
                    audioManager?.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_RAISE, 0)
                    val vol = audioManager?.getStreamVolume(AudioManager.STREAM_MUSIC) ?: 0
                    showVolumeIndicator(vol)
                    return true
                }
                KeyEvent.KEYCODE_VOLUME_DOWN -> {
                    audioManager?.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_LOWER, 0)
                    val vol = audioManager?.getStreamVolume(AudioManager.STREAM_MUSIC) ?: 0
                    showVolumeIndicator(vol)
                    return true
                }
            }
            return super.onKeyDown(keyCode, event)
        }

        // TV 模式: D-pad 控制
        val reverseChannels = getSharedPreferences(Prefs.FILE, MODE_PRIVATE).getBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, false)
        when (keyCode) {
            KeyEvent.KEYCODE_CHANNEL_UP, KeyEvent.KEYCODE_PAGE_UP,
            KeyEvent.KEYCODE_DPAD_RIGHT -> {
                if (reverseChannels) prevChannel() else nextChannel()
                return true
            }

            KeyEvent.KEYCODE_CHANNEL_DOWN, KeyEvent.KEYCODE_PAGE_DOWN,
            KeyEvent.KEYCODE_DPAD_LEFT -> {
                if (reverseChannels) nextChannel() else prevChannel()
                return true
            }

            KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER -> {
                toggleChannelInfo(); return true
            }

            KeyEvent.KEYCODE_DPAD_UP -> {
                // 音量+
                audioManager?.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_RAISE, 0)
                return true
            }

            KeyEvent.KEYCODE_DPAD_DOWN -> {
                // 音量-
                audioManager?.adjustStreamVolume(AudioManager.STREAM_MUSIC, AudioManager.ADJUST_LOWER, 0)
                return true
            }

            KeyEvent.KEYCODE_MEDIA_PLAY_PAUSE -> {
                playerHelper?.let { if (it.isPlaying()) it.pause() else it.resume() }; return true
            }
            KeyEvent.KEYCODE_MEDIA_STOP -> { finish(); return true }
            KeyEvent.KEYCODE_BACK -> {
                if (backPressedTime + 2000 > System.currentTimeMillis()) {
                    saveProgress(); finish()
                } else {
                    Toast.makeText(this, getString(R.string.toast_player_press_back_again), Toast.LENGTH_SHORT).show()
                    backPressedTime = System.currentTimeMillis()
                }
                return true
            }

            // 数字键直接跳转频道
            in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> {
                // val num = keyCode - KeyEvent.KEYCODE_0
                // 可扩展：输入频道号跳转
                return true
            }
        }
        return super.onKeyDown(keyCode, event)
    }

    // ═══════════════════════════════════════════════════
    // LIFECYCLE
    // ═══════════════════════════════════════════════════

    override fun onPause() {
        super.onPause()
        
        // 如果处于画中画模式，保持小窗渲染，无需启动后台服务
        if (pipController.shouldKeepPlayerAliveOnPause()) {
            return
        }
        
        // 如果是因为关闭画中画而导致的 onPause，不启动后台服务
        if (isPipClosedBySystem || isFinishing) {
            return
        }
        
        // 不暂停 player，让它在后台继续播放
        // 启动前台服务保活进程
        val serviceIntent = Intent(this, PlaybackService::class.java).apply {
            putExtra("channel_name", channelName)
        }
        try { androidx.core.content.ContextCompat.startForegroundService(this, serviceIntent) } catch (_: Exception) {}
    }

    override fun onStop() {
        super.onStop()
        if (isPipClosedBySystem || (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N && isInPictureInPictureMode)) {
            playerHelper?.release()
            pipController.release()
            stopService(Intent(this, PlaybackService::class.java))
            lifecycleScope.launch {
                com.mediaplayer.app.util.PlaybackStateReporter.reportStopped()
            }
            finishAffinity()
        }
    }

    // onResume is handled earlier

    override fun onDestroy() {
        super.onDestroy()
        com.mediaplayer.app.util.NsdHelper.unregisterService(this)
        stopService(Intent(this, PlaybackService::class.java))
        handler.removeCallbacksAndMessages(null)
        pipController.release()
        playerHelper?.release()
        playerHelper = null
        lifecycleScope.launch {
            com.mediaplayer.app.util.PlaybackStateReporter.reportStopped()
        }
    }

    override fun onPipPlay() {
        playerHelper?.resume()
        pipController.updatePipParams(true)
    }

    override fun onPipPause() {
        playerHelper?.pause()
        pipController.updatePipParams(false)
    }

    override fun onPipNext() {
        nextChannel()
        pipController.updatePipParams(true)
    }

    override fun onPipPrev() {
        prevChannel()
        pipController.updatePipParams(true)
    }
}
