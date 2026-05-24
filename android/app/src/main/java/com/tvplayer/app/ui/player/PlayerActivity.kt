package com.tvplayer.app.ui.player

import android.content.Intent
import android.media.AudioManager
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
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.interfaces.IMedia
import org.videolan.libvlc.util.VLCVideoLayout
import com.tvplayer.app.Prefs
import com.tvplayer.app.R
import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.api.ClientAuthManager
import com.tvplayer.app.data.model.Channel
import com.tvplayer.app.data.repository.ChannelRepository
import com.tvplayer.app.service.PlaybackService
import com.tvplayer.app.util.DeviceUtils
import com.tvplayer.app.util.PlayerGestureController
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.min

@androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)
class PlayerActivity : AppCompatActivity() {

    private var libVlc: LibVLC? = null
    private var mediaPlayer: MediaPlayer? = null
    private val repo = ChannelRepository()
    private lateinit var authManager: ClientAuthManager
    private var isTvMode = false

    // ── Views ──
    private lateinit var videoLayout: VLCVideoLayout
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
    private var allChannels = listOf<Channel>()

    private val handler = Handler(Looper.getMainLooper())
    private val hideInfoRunnable = Runnable { hideChannelInfo() }
    private val hideVolumeRunnable = Runnable { layoutVolumeIndicator?.visibility = View.GONE }
    private val hideBrightnessRunnable = Runnable { layoutBrightnessIndicator?.visibility = View.GONE }
    private val hideSpeedRunnable = Runnable { tvSpeedIndicator?.visibility = View.GONE }

    // ── Retry ──
    private var retryCount = 0
    private val maxRetries = 3
    private val baseRetryDelay = 3000L // 3秒
    private var backPressedTime = 0L

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        isTvMode = DeviceUtils.isTV(this)

        if (isTvMode) {
            setContentView(R.layout.activity_player)
            setupTvPlayerViews()
        } else {
            setContentView(R.layout.activity_player_phone)
            setupPhonePlayerViews()
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
        val userAgent = intent.getStringExtra("user_agent") ?: ""
        val customHeaders = intent.getStringExtra("custom_headers") ?: ""

        initPlayer()
        playStream(streamUrl, streamType, userAgent, customHeaders)
        loadChannels()
        showChannelInfo()
    }

    // ═══════════════════════════════════════════════════
    // VIEW SETUP
    // ═══════════════════════════════════════════════════

    private fun setupTvPlayerViews() {
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
    }

    private fun setupPhonePlayerViews() {
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
                mediaPlayer?.let { if (it.isPlaying) it.pause() else it.play() }
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
                mediaPlayer?.let { p ->
                    val newPos = max(0, p.time + deltaMs)
                    p.time = newPos
                }
            }

            override fun onLongPressStart() {
                isLongPressingSpeed = true
                mediaPlayer?.rate = 2.0f
                showSpeedIndicator("2.0x ▶▶")
            }

            override fun onLongPressEnd() {
                isLongPressingSpeed = false
                mediaPlayer?.rate = 1.0f
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

    private fun initPlayer() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)

        val options = ArrayList<String>()
        options.add("--aout=opensles")
        options.add("--audio-time-stretch")
        options.add("--drop-late-frames")
        options.add("--skip-frames")
        options.add("--network-caching=1500")

        when (decoderMode) {
            Prefs.DECODER_MODE_HARDWARE -> {
                options.add("--avcodec-hw=any")
                options.add("--codec=mediacodec,all")
            }
            Prefs.DECODER_MODE_SOFTWARE -> {
                options.add("--avcodec-hw=none")
            }
            else -> { // AUTO
                options.add("--vout=android_display")
                options.add("--avcodec-hw=any")
            }
        }

        libVlc = LibVLC(this, options)
        mediaPlayer = MediaPlayer(libVlc)
        mediaPlayer?.attachViews(videoLayout, null, false, false)

        mediaPlayer?.setEventListener { event ->
            when (event.type) {
                MediaPlayer.Event.Buffering -> {
                    if (event.buffering == 100f) {
                        progressBar?.visibility = View.GONE
                        tvStatus?.text = "播放中"
                        retryCount = 0
                        handler.postDelayed({ hideChannelInfo() }, 3000)
                    } else {
                        progressBar?.visibility = View.VISIBLE
                        tvStatus?.text = "缓冲中... ${event.buffering.toInt()}%"
                    }
                }
                MediaPlayer.Event.Playing -> {
                    progressBar?.visibility = View.GONE
                    tvStatus?.text = "播放中"
                    retryCount = 0
                    
                    var videoRes = ""
                    var audioCodec = ""
                    mediaPlayer?.media?.let { media ->
                        for (i in 0 until media.trackCount) {
                            val track = media.getTrack(i)
                            if (track.type == IMedia.Track.Type.Video) {
                                val vt = track as IMedia.VideoTrack
                                if (vt.width > 0 && vt.height > 0) {
                                    videoRes = "${vt.width}x${vt.height}"
                                }
                            } else if (track.type == IMedia.Track.Type.Audio) {
                                val at = track as IMedia.AudioTrack
                                audioCodec = at.codec?.trim()?.uppercase() ?: ""
                            }
                        }
                    }
                    val info = buildString {
                        if (videoRes.isNotEmpty()) append(videoRes)
                        if (videoRes.isNotEmpty() && audioCodec.isNotEmpty()) append(" | ")
                        if (audioCodec.isNotEmpty()) append(audioCodec)
                    }
                    if (info.isNotEmpty()) {
                        tvResolution?.text = info
                    }
                }
                MediaPlayer.Event.EncounteredError -> {
                    tvStatus?.text = "播放失败"
                    progressBar?.visibility = View.GONE
                    if (retryCount < maxRetries) {
                        retryCount++
                        val delayMs = (3000L * (1 shl (retryCount - 1)))
                        Toast.makeText(this@PlayerActivity, "播放失败，${delayMs/1000}秒后重试 ($retryCount/$maxRetries)...", Toast.LENGTH_SHORT).show()
                        handler.postDelayed({ retryPlay() }, delayMs)
                    } else {
                        Toast.makeText(this@PlayerActivity, "播放失败，已重试${maxRetries}次", Toast.LENGTH_LONG).show()
                        retryCount = 0
                    }
                }
            }
        }
    }

    private fun playStream(url: String, type: String, userAgent: String = "", customHeaders: String = "") {
        val player = mediaPlayer ?: return
        retryCount = 0
        progressBar?.visibility = View.VISIBLE
        tvChannelName?.text = channelName
        tvStreamType?.text = type.uppercase()

        val media = Media(libVlc, Uri.parse(url))
        
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        when (decoderMode) {
            Prefs.DECODER_MODE_HARDWARE -> media.setHWDecoderEnabled(true, true)
            Prefs.DECODER_MODE_SOFTWARE -> media.setHWDecoderEnabled(false, false)
            else -> media.setHWDecoderEnabled(true, false) // 自动
        }
        
        applyMediaOptions(media, userAgent, customHeaders)
        player.media = media
        player.play()
    }

    private fun applyMediaOptions(media: org.videolan.libvlc.Media, userAgent: String?, customHeaders: String?) {
        if (!userAgent.isNullOrEmpty()) {
            media.addOption(":http-user-agent=$userAgent")
        }
        if (!customHeaders.isNullOrEmpty()) {
            try {
                val json = org.json.JSONObject(customHeaders)
                val keys = json.keys()
                while (keys.hasNext()) {
                    val key = keys.next()
                    val value = json.getString(key)
                    if (key.equals("referer", ignoreCase = true) || key.equals("referrer", ignoreCase = true)) {
                        media.addOption(":http-referrer=$value")
                    } else if (key.equals("origin", ignoreCase = true)) {
                        media.addOption(":http-origin=$value")
                    } else if (key.equals("cookie", ignoreCase = true)) {
                        media.addOption(":http-cookies=$value")
                    }
                }
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
    }

    private fun retryPlay() {
        val channel = allChannels.getOrNull(channelIndex)
        if (channel != null) {
            playStream(channel.streamUrl, channel.streamType, channel.userAgent, channel.customHeaders)
        } else {
            val ua = intent.getStringExtra("user_agent") ?: ""
            val headers = intent.getStringExtra("custom_headers") ?: ""
            playStream(streamUrl, streamType, ua, headers)
        }
    }

    private fun loadChannels() {
        lifecycleScope.launch {
            repo.getChannels().onSuccess { allChannels = it }
        }
    }

    // ═══════════════════════════════════════════════════
    // CHANNEL SWITCHING
    // ═══════════════════════════════════════════════════

    private fun switchChannel(index: Int) {
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return

        channelIndex = index
        val channel = allChannels[index]
        channelId = channel.id
        channelName = channel.name
        streamUrl = channel.streamUrl
        streamType = channel.streamType

        playStream(streamUrl, streamType, channel.userAgent, channel.customHeaders)
        showChannelInfo()
    }

    private fun nextChannel() {
        if (allChannels.isEmpty()) {
            Toast.makeText(this, "频道列表加载中...", Toast.LENGTH_SHORT).show()
            return
        }
        if (channelIndex < allChannels.size - 1) switchChannel(channelIndex + 1)
        else switchChannel(0) // 循环到第一个
    }

    private fun prevChannel() {
        if (allChannels.isEmpty()) {
            Toast.makeText(this, "频道列表加载中...", Toast.LENGTH_SHORT).show()
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
        loadEPG()
        handler.removeCallbacks(hideInfoRunnable)
        handler.postDelayed(hideInfoRunnable, 5000)
    }

    private fun loadEPG() {
        val channel = allChannels.getOrNull(channelIndex)
        if (channel != null && channel.currentEpg.isNotEmpty()) {
            layoutEpg?.visibility = View.VISIBLE
            tvEpgNow?.text = "📺 正在播放: ${channel.currentEpg}"
            tvEpgNext?.text = ""
        } else {
            layoutEpg?.visibility = View.GONE
        }
    }

    private fun hideChannelInfo() {
        layoutChannelInfo?.visibility = View.GONE
    }

    private fun toggleChannelInfo() {
        if (layoutChannelInfo?.visibility == View.VISIBLE) hideChannelInfo() else showChannelInfo()
    }

    private fun showVolumeIndicator(vol: Int) {
        progressVolume?.progress = (vol * 100 / maxVolume)
        tvVolume?.text = "🔊 $vol"
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
        val pos = mediaPlayer?.time?.div(1000)?.toInt() ?: 0
        val clientId = authManager.getClientId()
        lifecycleScope.launch { repo.addHistory(channelId, pos, pos, clientId) }
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
                        Toast.makeText(this, "再按一次返回键退出播放", Toast.LENGTH_SHORT).show()
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
        when (keyCode) {
            KeyEvent.KEYCODE_CHANNEL_UP, KeyEvent.KEYCODE_PAGE_UP,
            KeyEvent.KEYCODE_DPAD_RIGHT -> { nextChannel(); return true }

            KeyEvent.KEYCODE_CHANNEL_DOWN, KeyEvent.KEYCODE_PAGE_DOWN,
            KeyEvent.KEYCODE_DPAD_LEFT -> { prevChannel(); return true }

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
                mediaPlayer?.let { if (it.isPlaying) it.pause() else it.play() }; return true
            }
            KeyEvent.KEYCODE_MEDIA_STOP -> { finish(); return true }
            KeyEvent.KEYCODE_BACK -> {
                if (backPressedTime + 2000 > System.currentTimeMillis()) {
                    saveProgress(); finish()
                } else {
                    Toast.makeText(this, "再按一次返回键退出播放", Toast.LENGTH_SHORT).show()
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
        // 不暂停 player，让它在后台继续播放
        // 启动前台服务保活进程
        val serviceIntent = Intent(this, PlaybackService::class.java).apply {
            putExtra("channel_name", channelName)
        }
        try { androidx.core.content.ContextCompat.startForegroundService(this, serviceIntent) } catch (_: Exception) {}
    }

    override fun onResume() {
        super.onResume()
        // 停止保活服务
        stopService(Intent(this, PlaybackService::class.java))
        mediaPlayer?.play()
        hideSystemUI()
    }

    override fun onDestroy() {
        super.onDestroy()
        stopService(Intent(this, PlaybackService::class.java))
        handler.removeCallbacksAndMessages(null)
        mediaPlayer?.release()
        libVlc?.release()
        mediaPlayer = null
        libVlc = null
    }
}
