package com.mediaplayer.app.ui.home

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.Editable
import android.text.TextWatcher
import android.view.KeyEvent
import android.view.LayoutInflater
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.HorizontalScrollView
import android.widget.SeekBar
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.lifecycle.lifecycleScope
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.data.api.ApiClient
import com.mediaplayer.app.data.api.ClientAuthManager
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelGroup
import com.mediaplayer.app.data.model.ChannelLine
import com.mediaplayer.app.data.repository.ChannelRepository
import com.mediaplayer.app.ui.player.PlayerActivity
import com.mediaplayer.app.ui.settings.SettingsActivity
import com.mediaplayer.app.util.DeviceUtils
import com.mediaplayer.app.util.FocusHelper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import com.mediaplayer.app.util.VlcPlayerHelper
import org.videolan.libvlc.util.VLCVideoLayout
import kotlin.math.max

class MainActivity : AppCompatActivity() {

    private val repo = ChannelRepository()
    private lateinit var authManager: ClientAuthManager
    private var isTvMode = true

    // ── Views (TV mode - Zapping & Player) ──
    private var tvGroupsRv: RecyclerView? = null
    private var tvChannelsRv: RecyclerView? = null
    private var tvAuthWaiting: View? = null
    private var layoutZappingMenu: View? = null
    private var layoutOsd: View? = null
    private var tvOsdChannelNum: TextView? = null
    private var tvOsdChannelName: TextView? = null
    private var tvOsdInfo: TextView? = null
    private var tvOsdEpg: TextView? = null
    private var progressEpg: ProgressBar? = null
    private var progressBuffering: ProgressBar? = null
    private var videoLayout: android.widget.FrameLayout? = null
    
    // EPG Menu
    private var layoutEpgMenu: View? = null
    private var rvEpgList: androidx.recyclerview.widget.RecyclerView? = null
    private var progressEpgLoading: View? = null
    private var tvEpgEmptyText: TextView? = null
    private var tvEpgMenuTitle: TextView? = null
    private lateinit var epgAdapter: EpgAdapter
    
    // Line Selection Menu
    private var layoutLineMenu: View? = null
    private var tvLineMenuTitle: TextView? = null
    private var containerLines: LinearLayout? = null

    // ── Settings Sidebar ──
    private var layoutSettingsMenu: View? = null
    private var etSettingsUrl: EditText? = null
    private var sbSettingsCache: android.widget.SeekBar? = null
    private var tvSettingsCacheValue: TextView? = null

    private var tvSettingsInfo: TextView? = null

    // QR Code Config
    private var configWebServer: com.mediaplayer.app.server.ConfigWebServer? = null
    private var layoutQrConfig: View? = null
    private var ivQrCode: android.widget.ImageView? = null
    private var tvQrConfigHint: TextView? = null
    
    private var layoutAuthQrConfig: View? = null
    private var ivAuthQrCode: android.widget.ImageView? = null
    private var tvAuthQrConfigHint: TextView? = null

    // ── Views (Phone mode) ──
    private var phoneGroupTabs: LinearLayout? = null
    private var phoneChannelsRv: RecyclerView? = null
    private var phoneChannelCount: TextView? = null
    private var phoneAuthWaiting: View? = null
    private var phoneContent: View? = null
    private var phoneSearchLayout: View? = null
    private var phoneSearchEdit: EditText? = null
    private var phoneScrollView: HorizontalScrollView? = null
    private var phoneSwipeRefresh: SwipeRefreshLayout? = null

    // ── Shared loading/empty views ──
    private var progressLoading: ProgressBar? = null
    private var layoutEmpty: View? = null
    private var tvEmptyText: TextView? = null

    // ── Catchup State ──
    private var currentCatchupStartTime: String? = null
    private var currentCatchupChannelIndex: Int = -1

    // ── Data ──
    private var groups = listOf<ChannelGroup>()
    private var allChannels = listOf<Channel>()
    private var filteredChannels = listOf<Channel>()
    private var currentGroupId = 0L
    private var currentChannelIndex = 0
    private var currentLineIndex = 0
    private var coreRetryLevel = 0 // 0=default, 1=Exo, 2=VLC

    private lateinit var groupAdapter: GroupAdapter
    private lateinit var channelAdapter: ChannelAdapter

    private val authPollHandler = Handler(Looper.getMainLooper())
    private var authPollRunnable: Runnable? = null
    private val heartbeatHandler = Handler(Looper.getMainLooper())
    private var heartbeatRunnable: Runnable? = null

    // ── VLC Player (TV Only) ──
    private var playerHelper: com.mediaplayer.app.util.IPlayerHelper? = null
    private var retryCount = 0
    private val maxRetries = 3
    private val uiHandler = Handler(Looper.getMainLooper())
    
    // Watchdog State
    private var isPlayerBuffering = false
    private var lastPlaybackTime = 0L
    private var frozenTimeCounter = 0
    private val watchdogRunnable = object : Runnable {
        override fun run() {
            if (isTvMode && playerHelper?.isPlaying() == true && !isPlayerBuffering) {
                val currentTime = playerHelper?.getTime() ?: 0L
                if (currentTime > 0 && currentTime == lastPlaybackTime) {
                    frozenTimeCounter++
                    if (frozenTimeCounter >= 2) {
                        Toast.makeText(this@MainActivity, "检测到流卡死，正在尝试恢复...", Toast.LENGTH_SHORT).show()
                        playCurrentLineInTv()
                        frozenTimeCounter = 0
                        return
                    }
                } else {
                    lastPlaybackTime = currentTime
                    frozenTimeCounter = 0
                }
            }
            uiHandler.postDelayed(this, 5000)
        }
    }

    private val hideOsdRunnable = Runnable { layoutOsd?.visibility = View.GONE }
    private val hideZappingRunnable = Runnable { 
        layoutZappingMenu?.visibility = View.GONE 
        videoLayout?.requestFocus()
    }

    // System Announcement state
    private var sysAnnouncement: String? = null
    private var sysAnnouncementInterval: Int = 0
    private var marqueeIsVisible = false
    private val marqueeRunnable = Runnable { triggerMarquee() }
    private val hideMarqueeRunnable = Runnable { 
        findViewById<android.view.View>(R.id.layoutAnnouncement)?.visibility = View.GONE
        findViewById<android.widget.TextView>(R.id.tvAnnouncement)?.isSelected = false
        marqueeIsVisible = false
        if (sysAnnouncementInterval > 0) {
            uiHandler.postDelayed(marqueeRunnable, sysAnnouncementInterval * 60 * 1000L)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        super.onCreate(savedInstanceState)

        // 强制所有设备使用 TV 的沉浸式界面大一统！
        isTvMode = true
        
        // 保持屏幕常亮，防止手机/Pad自动锁屏
        window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        // 兼容刘海屏/挖孔屏/灵动岛：允许画面延伸到全部屏幕边缘
        // Android 15+ (API 35): ALWAYS 模式确保长边（灵动岛）区域也被覆盖
        // Android 9-14 (API 28-34): SHORT_EDGES 已足够覆盖所有刘海/挖孔场景
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P) {
            val lp = window.attributes
            lp.layoutInDisplayCutoutMode = if (android.os.Build.VERSION.SDK_INT >= 35) {
                // LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS = 3 (Android 15+)
                3
            } else {
                android.view.WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
            }
            window.attributes = lp
        }

        // 强行关闭系统布局自适应，允许布局内容延伸到状态栏和导航栏区域下
        WindowCompat.setDecorFitsSystemWindows(window, false)
        hideSystemUI()

        setContentView(R.layout.activity_main)
        setupTvViews()
        // Player will be initialized when playing a channel
        setupTouchGestures()

        authManager = ClientAuthManager(this)

        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val serverUrl = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: Prefs.DEFAULT_SERVER_URL
        ApiClient.init(serverUrl)

        setupAdapters()
        checkAuthAndLoad()
        
        // 检查版本更新
        com.mediaplayer.app.util.UpdateManager.checkUpdate(this, lifecycleScope, false)
    }

    private fun hideSystemUI() {
        WindowInsetsControllerCompat(window, window.decorView).let { controller ->
            controller.hide(WindowInsetsCompat.Type.systemBars())
            controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }
    }

    // ── Touch Gestures for Mobile/Tablet ──
    private fun setupTouchGestures() {
        val gestureDetector = android.view.GestureDetector(this, object : android.view.GestureDetector.SimpleOnGestureListener() {
            override fun onDown(e: android.view.MotionEvent): Boolean {
                return true
            }

            override fun onSingleTapConfirmed(e: android.view.MotionEvent): Boolean {
                val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
                if (!isMenuVisible) {
                    layoutZappingMenu?.visibility = View.VISIBLE
                    
                    val playingId = if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) allChannels[currentChannelIndex].id else -1L
                    val indexInFiltered = filteredChannels.indexOfFirst { it.id == playingId }
                    if (indexInFiltered >= 0) {
                        tvChannelsRv?.scrollToPosition(indexInFiltered)
                        tvChannelsRv?.postDelayed({
                            val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                            lm?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                        }, 50)
                    } else {
                        tvChannelsRv?.requestFocus()
                    }

                    uiHandler.removeCallbacks(hideZappingRunnable)
                    uiHandler.postDelayed(hideZappingRunnable, 10000)
                } else {
                    uiHandler.removeCallbacks(hideZappingRunnable)
                    hideZappingRunnable.run()
                }
                return true
            }

            override fun onLongPress(e: android.view.MotionEvent) {
                // 手机端：长按屏幕呼出“手动切换线路”
                showLineSelectionMenu()
            }

            override fun onDoubleTap(e: android.view.MotionEvent): Boolean {
                // 手机端：双击屏幕呼出“设置菜单”
                val isSettingsVisible = layoutSettingsMenu?.visibility == View.VISIBLE
                if (isSettingsVisible) hideSettingsMenu() else showSettingsMenu()
                return true
            }

            override fun onFling(e1: android.view.MotionEvent?, e2: android.view.MotionEvent, velocityX: Float, velocityY: Float): Boolean {
                if (e1 == null) return false
                val deltaY = e2.y - e1.y
                val deltaX = e2.x - e1.x
                
                // 上下滑动：切台
                if (kotlin.math.abs(deltaY) > kotlin.math.abs(deltaX) && kotlin.math.abs(deltaY) > 100) {
                    if (layoutZappingMenu?.visibility == View.VISIBLE || layoutEpgMenu?.visibility == View.VISIBLE) return false
                    if (deltaY > 0) {
                        // 向下滑动：上一个频道
                        val prev = if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        playTvChannel(prev)
                    } else {
                        // 向上滑动：下一个频道
                        val next = if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        playTvChannel(next)
                    }
                    return true
                } 
                // 左右滑动
                else if (kotlin.math.abs(deltaX) > kotlin.math.abs(deltaY) && kotlin.math.abs(deltaX) > 100) {
                    if (deltaX < 0) {
                        // 向左滑动：呼出右侧的 EPG 菜单
                        if (layoutZappingMenu?.visibility != View.VISIBLE && layoutSettingsMenu?.visibility != View.VISIBLE) {
                            showEpgMenu()
                            return true
                        }
                    } else {
                        // 向右滑动：如果是 EPG 面板，则关闭它
                        if (layoutEpgMenu?.visibility == View.VISIBLE) {
                            hideEpgMenu()
                            return true
                        }
                    }
                }
                return false
            }
        })

        // 在最顶层的 AuthWaiting 后加一个透明遮罩层或者直接接管 Activity 的 onTouchEvent 比较困难
        // 最优雅的方法是给 videoLayout 所在的父布局加拦截，但由于 VLCVideoLayout 结构特殊，
        // 我们可以在 rootView 上监听，或者给 videoLayout 设置
        videoLayout?.setOnTouchListener { _, event ->
            gestureDetector.onTouchEvent(event)
            true // 消费掉触摸事件
        }
    }

    // ═══════════════════════════════════════════════════
    // TV MODE SETUP & PLAYER
    // ═══════════════════════════════════════════════════

    private fun setupTvViews() {
        tvGroupsRv = findViewById(R.id.rvGroups)
        tvChannelsRv = findViewById(R.id.rvChannels)
        tvAuthWaiting = findViewById(R.id.layoutAuthWaiting)
        layoutZappingMenu = findViewById(R.id.layoutZappingMenu)
        layoutOsd = findViewById(R.id.layoutOsd)
        tvOsdChannelNum = findViewById(R.id.tvOsdChannelNum)
        tvOsdChannelName = findViewById(R.id.tvOsdChannelName)
        tvOsdInfo = findViewById(R.id.tvOsdInfo)
        tvOsdEpg = findViewById(R.id.tvOsdEpg)
        progressEpg = findViewById(R.id.progressEpg)
        progressBuffering = findViewById(R.id.progressBuffering)
        videoLayout = findViewById(R.id.videoLayout)
        progressLoading = findViewById(R.id.progressLoading)
        layoutEmpty = findViewById(R.id.layoutEmpty)
        tvEmptyText = findViewById(R.id.tvEmptyText)

        // Settings sidebar
        layoutSettingsMenu = findViewById(R.id.layoutSettingsMenu)
        etSettingsUrl = findViewById(R.id.etSettingsUrl)
        sbSettingsCache = findViewById(R.id.sbSettingsCache)
        tvSettingsCacheValue = findViewById(R.id.tvSettingsCacheValue)

        tvSettingsInfo = findViewById(R.id.tvSettingsInfo)

        layoutQrConfig = findViewById(R.id.layoutQrConfig)
        ivQrCode = findViewById(R.id.ivQrCode)
        tvQrConfigHint = findViewById(R.id.tvQrConfigHint)
        
        layoutAuthQrConfig = findViewById(R.id.layoutAuthQrConfig)
        ivAuthQrCode = findViewById(R.id.ivAuthQrCode)
        tvAuthQrConfigHint = findViewById(R.id.tvAuthQrConfigHint)

        // EPG Menu
        layoutEpgMenu = findViewById(R.id.layoutEpgMenu)
        rvEpgList = findViewById(R.id.rvEpgList)
        progressEpgLoading = findViewById(R.id.progressEpgLoading)
        tvEpgEmptyText = findViewById(R.id.tvEpgEmptyText)
        tvEpgMenuTitle = findViewById(R.id.tvEpgMenuTitle)
        
        epgAdapter = EpgAdapter()
        epgAdapter.setOnItemClickListener { prog ->
            val channel = allChannels.getOrNull(currentChannelIndex) ?: return@setOnItemClickListener
            try {
                val sdf = java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", java.util.Locale.getDefault())
                val startUnix = sdf.parse(prog.startTime)?.time?.div(1000) ?: 0L
                val endUnix = sdf.parse(prog.endTime)?.time?.div(1000) ?: 0L
                if (startUnix > 0 && endUnix > 0) {
                    val url = ApiClient.getCatchupUrl(channel.id, startUnix, endUnix)
                    val lines = channel.getLinesSafely()
                    val ua = if (lines.isNotEmpty()) lines[0].userAgent else ""
                    val headers = if (lines.isNotEmpty()) lines[0].customHeaders else ""
                    
                    if (isTvMode) {
                        currentCatchupStartTime = prog.startTime
                        currentCatchupChannelIndex = currentChannelIndex
                        tvOsdInfo?.text = "回看: ${prog.title}"
                        playerHelper?.play(url, ua, headers)
                        hideEpgMenu()
                    } else {
                        val intent = android.content.Intent(this, com.mediaplayer.app.ui.player.PlayerActivity::class.java).apply {
                            putExtra("channel_id", channel.id)
                            putExtra("channel_name", channel.name)
                            putExtra("stream_url", url)
                            putExtra("stream_type", "hls") // Catchup is usually HLS
                            putExtra("user_agent", ua)
                            putExtra("custom_headers", headers)
                        }
                        startActivity(intent)
                    }
                }
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
        rvEpgList?.layoutManager = androidx.recyclerview.widget.LinearLayoutManager(this)
        rvEpgList?.adapter = epgAdapter
        
        // Line Menu
        layoutLineMenu = findViewById(R.id.layoutLineMenu)
        tvLineMenuTitle = findViewById(R.id.tvLineMenuTitle)
        containerLines = findViewById(R.id.containerLines)

        setupSettingsViews()

        tvGroupsRv?.let { FocusHelper.setupTvRecyclerView(it) }
        tvChannelsRv?.let { FocusHelper.setupTvRecyclerView(it) }

        val groupsRv = tvGroupsRv
        val channelsRv = tvChannelsRv
        if (groupsRv != null && channelsRv != null) {
            FocusHelper.linkHorizontalFocus(groupsRv, channelsRv)
        }
    }

    private var btnSettingsScale: View? = null
    private var btnSettingsDecoder: View? = null
    private var btnSettingsCore: View? = null

    private fun setupSettingsViews() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val url = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL)
        
        btnSettingsDecoder = findViewById(R.id.btnSettingsDecoder)
        btnSettingsCore = findViewById(R.id.btnSettingsCore)
        btnSettingsScale = findViewById(R.id.btnSettingsScale)
        val btnSettingsAutoStart = findViewById<View>(R.id.btnSettingsAutoStart)
        
        fun updateDecoderText(mode: Int) {
            findViewById<TextView>(R.id.tvSettingsDecoderValue)?.text = when (mode) {
                Prefs.DECODER_MODE_HARDWARE -> "强制硬解"
                Prefs.DECODER_MODE_SOFTWARE -> "强制软解"
                else -> "自动识别"
            }
        }
        
        fun updateCoreText(core: Int) {
            findViewById<TextView>(R.id.tvSettingsCoreValue)?.text = when (core) {
                Prefs.PLAYER_CORE_VLC -> "VLC"
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                else -> "智能切换"
            }
        }

        fun updateScaleText(mode: Int) {
            findViewById<TextView>(R.id.tvSettingsScaleValue)?.text = when (mode) {
                Prefs.SCALE_MODE_STRETCH -> "强制 16:9"
                Prefs.SCALE_MODE_CROP -> "放大裁剪"
                Prefs.SCALE_MODE_4_3 -> "强制 4:3"
                else -> "原始比例"
            }
        }

        fun updateAutoStartText(enabled: Boolean) {
            findViewById<TextView>(R.id.tvSettingsAutoStartValue)?.text = if (enabled) "开" else "关"
        }
        
        var currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        var currentCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        if (currentCore == Prefs.PLAYER_CORE_X5) {
            currentCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, currentCore).apply()
        }
        var currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        var currentAutoStart = prefs.getBoolean(Prefs.KEY_AUTO_START, true)

        updateDecoderText(currentDecoderMode)
        updateCoreText(currentCore)
        updateScaleText(currentScaleMode)
        updateAutoStartText(currentAutoStart)
        
        btnSettingsDecoder?.setOnClickListener {
            currentDecoderMode = when (currentDecoderMode) {
                Prefs.DECODER_MODE_AUTO -> Prefs.DECODER_MODE_HARDWARE
                Prefs.DECODER_MODE_HARDWARE -> Prefs.DECODER_MODE_SOFTWARE
                else -> Prefs.DECODER_MODE_AUTO
            }
            updateDecoderText(currentDecoderMode)
            prefs.edit().putInt(Prefs.KEY_DECODER_MODE, currentDecoderMode).apply()
            Toast.makeText(this, "解码模式已保存，下次播放生效", Toast.LENGTH_SHORT).show()
        }
        
        btnSettingsCore?.setOnClickListener {
            currentCore = when (currentCore) {
                Prefs.PLAYER_CORE_AUTO -> Prefs.PLAYER_CORE_EXO
                Prefs.PLAYER_CORE_EXO -> Prefs.PLAYER_CORE_VLC
                else -> Prefs.PLAYER_CORE_AUTO
            }
            updateCoreText(currentCore)
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, currentCore).apply()
            Toast.makeText(this, "播放内核已保存，下次播放生效", Toast.LENGTH_SHORT).show()
        }
        
        btnSettingsScale?.setOnClickListener {
            currentScaleMode = when (currentScaleMode) {
                Prefs.SCALE_MODE_DEFAULT -> Prefs.SCALE_MODE_STRETCH
                Prefs.SCALE_MODE_STRETCH -> Prefs.SCALE_MODE_CROP
                Prefs.SCALE_MODE_CROP -> Prefs.SCALE_MODE_4_3
                else -> Prefs.SCALE_MODE_DEFAULT
            }
            updateScaleText(currentScaleMode)
            prefs.edit().putInt(Prefs.KEY_SCALE_MODE, currentScaleMode).apply()
            
            // 立即生效部分
            when (currentScaleMode) {
                Prefs.SCALE_MODE_STRETCH -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_STRETCH)
                Prefs.SCALE_MODE_4_3 -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_4_3)
                Prefs.SCALE_MODE_DEFAULT -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_DEFAULT)
                Prefs.SCALE_MODE_CROP -> {
                    playerHelper?.setAspectRatio(Prefs.SCALE_MODE_CROP)
                    Toast.makeText(this, "画面比例已保存，裁剪模式需重新播放生效", Toast.LENGTH_SHORT).show()
                }
            }
        }

        btnSettingsAutoStart?.setOnClickListener {
            currentAutoStart = !currentAutoStart
            updateAutoStartText(currentAutoStart)
            prefs.edit().putBoolean(Prefs.KEY_AUTO_START, currentAutoStart).apply()
        }

        val btnSettingsCheckUpdate = findViewById<View>(R.id.btnSettingsCheckUpdate)
        btnSettingsCheckUpdate?.setOnClickListener {
            com.mediaplayer.app.util.UpdateManager.checkUpdate(this, lifecycleScope, true)
        }

        etSettingsUrl?.setText(url)
        
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        val progress = if (cacheMs == 0) 0 else (cacheMs / 40).coerceIn(1, 125)
        sbSettingsCache?.max = 125 // 5000ms / 40 = 125
        sbSettingsCache?.progress = progress
        tvSettingsCacheValue?.text = if (cacheMs == 0) " 自动" else " ${cacheMs / 1000f} 秒"

        sbSettingsCache?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                val newCacheMs = if (progress == 0) 0 else progress * 40
                tvSettingsCacheValue?.text = if (newCacheMs == 0) " 自动" else " ${newCacheMs / 1000f} 秒"
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {
                val p = seekBar?.progress ?: 0
                val newCacheMs = if (p == 0) 0 else p * 40
                prefs.edit().putInt(Prefs.KEY_NETWORK_CACHE, newCacheMs).apply()
                playerHelper?.setCacheDuration(newCacheMs)
                Toast.makeText(this@MainActivity, "网络缓存已保存，下次播放生效", Toast.LENGTH_SHORT).show()
            }
        })

        // 音量设置
        val audioManager = getSystemService(android.content.Context.AUDIO_SERVICE) as android.media.AudioManager
        val maxVolume = audioManager.getStreamMaxVolume(android.media.AudioManager.STREAM_MUSIC)
        val currentVolume = audioManager.getStreamVolume(android.media.AudioManager.STREAM_MUSIC)
        val sbVolume = findViewById<SeekBar>(R.id.sbSettingsVolume)
        sbVolume?.max = maxVolume
        sbVolume?.progress = currentVolume
        sbVolume?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser) {
                    audioManager.setStreamVolume(android.media.AudioManager.STREAM_MUSIC, progress, 0)
                }
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {}
        })

        // 亮度设置
        val sbBrightness = findViewById<SeekBar>(R.id.sbSettingsBrightness)
        sbBrightness?.max = 100
        sbBrightness?.progress = ((window.attributes.screenBrightness.coerceAtLeast(0.01f)) * 100).toInt().coerceIn(1, 100)
        sbBrightness?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser) {
                    val lp = window.attributes
                    lp.screenBrightness = max(0.01f, progress / 100f)
                    window.attributes = lp
                }
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {}
        })
        
        findViewById<TextView>(R.id.tvQQGroup)?.setOnClickListener {
            try {
                // 使用 mqqapi 协议直接唤起手机 QQ 加群页面
                val intent = Intent(Intent.ACTION_VIEW, Uri.parse("mqqapi://card/show_pslcard?src_type=internal&version=1&uin=864744268&card_type=group&source=qrcode"))
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                startActivity(intent)
            } catch (e: Exception) {
                // 未安装 QQ 或拉起失败，复制群号到剪贴板
                val clipboard = getSystemService(android.content.Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                val clip = android.content.ClipData.newPlainText("QQ群", "864744268")
                clipboard.setPrimaryClip(clip)
                Toast.makeText(this@MainActivity, "未检测到QQ应用，已复制群号: 864744268", Toast.LENGTH_SHORT).show()
            }
        }
    }

    private fun showSettingsMenu() {
        if (layoutSettingsMenu?.visibility == View.VISIBLE) return
        
        // 隐藏左侧菜单
        layoutZappingMenu?.visibility = View.GONE
        
        layoutSettingsMenu?.visibility = View.VISIBLE
        // 填充关于信息
        val authManager = ClientAuthManager(this)
        var versionText = "1.0.0"
        try {
            val pInfo = packageManager.getPackageInfo(packageName, 0)
            val vCode = androidx.core.content.pm.PackageInfoCompat.getLongVersionCode(pInfo)
            versionText = "${pInfo.versionName} ($vCode)"
        } catch (_: Exception) {}
        
        val authStatus = when (authManager.getStatus()) {
            "approved" -> "已授权"
            "pending" -> "等待审批"
            "rejected" -> "已拒绝"
            "banned" -> "已封禁"
            "expired" -> "已过期"
            else -> "未注册"
        }
        tvSettingsInfo?.text = "应用版本: $versionText\n设备 ID: ${authManager.getDeviceId()}\n授权状态: $authStatus"
        
        // --- QR Code Logic ---
        val ip = com.mediaplayer.app.util.NetworkUtils.getLocalIpAddress()
        if (ip != null) {
            setupQrConfigServer {
                hideSettingsMenu()
                Toast.makeText(this@MainActivity, "配置已保存，重新加载中...", Toast.LENGTH_LONG).show()
                checkAuthAndLoad()
            }
            val qrUrl = "http://$ip:9528/"
            val bitmap = com.mediaplayer.app.util.QRCodeHelper.generateQRCode(qrUrl, 400)
            ivQrCode?.setImageBitmap(bitmap)
            tvQrConfigHint?.text = "手机扫码快速配置服务器\n或者访问: $qrUrl"
            tvQrConfigHint?.setOnClickListener {
                try {
                    val intent = android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(qrUrl))
                    intent.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
                    startActivity(intent)
                } catch (e: Exception) {
                    e.printStackTrace()
                }
            }
            layoutQrConfig?.visibility = View.VISIBLE
        } else {
            layoutQrConfig?.visibility = View.GONE
        }
        
        sbSettingsCache?.requestFocus()
    }

    private fun initPlayerWithCore(core: Int) {
        val listener = object : com.mediaplayer.app.util.IPlayerHelper.PlayerListener {
            override fun onBuffering(percent: Float) {
                uiHandler.post {
                    if (percent >= 100f || percent == 0f) {
                        progressBuffering?.visibility = View.GONE
                    } else {
                        progressBuffering?.visibility = View.VISIBLE
                    }
                }
            }
            override fun onPlaying(resolution: String) {
                uiHandler.post {
                    progressBuffering?.visibility = View.GONE
                    retryCount = 0
                    if (resolution.isNotEmpty()) {
                        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
                        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
                        val decoderStr = when (decoderMode) {
                            Prefs.DECODER_MODE_HARDWARE -> "硬解"
                            Prefs.DECODER_MODE_SOFTWARE -> "软解"
                            else -> "自动解码"
                        }
                        val coreStr = when (core) {
                            Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                            Prefs.PLAYER_CORE_X5 -> "X5 Web"
                            else -> "VLC"
                        }
                        
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
                        tvOsdInfo?.text = fullInfo
                    }
                }
            }
            override fun onError() {
                uiHandler.post { handlePlaybackError() }
            }
        }

        when (core) {
            Prefs.PLAYER_CORE_EXO -> {
                playerHelper = com.mediaplayer.app.util.ExoPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
            }
            Prefs.PLAYER_CORE_X5 -> {
                playerHelper = com.mediaplayer.app.util.X5PlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
            }
            else -> {
                val vlcVideoLayout = org.videolan.libvlc.util.VLCVideoLayout(this)
                vlcVideoLayout.layoutParams = android.widget.FrameLayout.LayoutParams(android.widget.FrameLayout.LayoutParams.MATCH_PARENT, android.widget.FrameLayout.LayoutParams.MATCH_PARENT)
                videoLayout?.addView(vlcVideoLayout)
                
                playerHelper = com.mediaplayer.app.util.VlcPlayerHelper(this, vlcVideoLayout, listener)
            }
        }
    }

    private fun setupQrConfigServer(onUrlUpdated: () -> Unit) {
        if (configWebServer == null) {
            configWebServer = com.mediaplayer.app.server.ConfigWebServer(this, 9528) { rawUrl ->
                runOnUiThread {
                    val newUrl = com.mediaplayer.app.data.api.ApiClient.formatUrl(rawUrl)
                    val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
                    prefs.edit().putString(Prefs.KEY_SERVER_URL, newUrl).apply()
                    com.mediaplayer.app.data.api.ApiClient.init(newUrl)
                    authManager.clearAuth()
                    onUrlUpdated()
                }
            }
            try {
                configWebServer?.start()
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
    }

    private fun hideSettingsMenu() {
        layoutSettingsMenu?.visibility = View.GONE
        videoLayout?.requestFocus()
    }

    private fun handlePlaybackError() {
        isPlayerBuffering = false
        progressBuffering?.visibility = View.GONE
        
        val channel = allChannels.getOrNull(currentChannelIndex)
        val lines = channel?.getLinesSafely() ?: emptyList()
        
        if (coreRetryLevel == 0) {
            coreRetryLevel = 1
            Toast.makeText(this@MainActivity, "尝试使用 ExoPlayer 重试该线路...", Toast.LENGTH_SHORT).show()
            playCurrentLineInTv()
        } else if (coreRetryLevel == 1) {
            coreRetryLevel = 2
            Toast.makeText(this@MainActivity, "尝试使用 VLC 重试该线路...", Toast.LENGTH_SHORT).show()
            playCurrentLineInTv()
        } else if (lines.isNotEmpty() && currentLineIndex < lines.size - 1) {
            currentLineIndex++
            coreRetryLevel = 0
            Toast.makeText(this@MainActivity, "当前线路完全失效，切换线路 ${currentLineIndex + 1}...", Toast.LENGTH_SHORT).show()
            playCurrentLineInTv()
        } else {
            Toast.makeText(this@MainActivity, "当前频道所有线路均无法播放", Toast.LENGTH_LONG).show()
            coreRetryLevel = 0
        }
    }

    private var resolveJob: kotlinx.coroutines.Job? = null

    private fun playTvChannel(index: Int) {
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return
        
        currentCatchupStartTime = null
        currentCatchupChannelIndex = -1
        
        if (currentChannelIndex != index) {
            currentLineIndex = 0
            coreRetryLevel = 0
        }
        currentChannelIndex = index
        playCurrentLineInTv()
    }
    
    private fun playCurrentLineInTv() {
        val channel = allChannels.getOrNull(currentChannelIndex) ?: return
        
        tvOsdChannelNum?.text = String.format("%03d", channel.globalIndex + 1)
        tvOsdChannelName?.text = channel.name
        
        val lines = channel.getLinesSafely()
        if (lines.isEmpty()) return
        if (currentLineIndex >= lines.size) currentLineIndex = 0
        val line = lines[currentLineIndex]
        
        tvOsdInfo?.text = if (lines.size > 1) "连接中... (线路 ${currentLineIndex + 1}/${lines.size})" else "连接中..."
        
        // 记忆功能：保存最后播放的频道 ID
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        prefs.edit().putLong("last_channel_id", channel.id).apply()
        
        loadEpgForChannel(channel)
        showOsd()

        // 核心匹配逻辑
        var globalCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        if (globalCore == Prefs.PLAYER_CORE_X5) {
            globalCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, globalCore).apply()
        }
        
        var desiredCore = globalCore
        var coreText = ""
        
        if (coreRetryLevel > 0) {
            desiredCore = if (coreRetryLevel == 1) Prefs.PLAYER_CORE_EXO else Prefs.PLAYER_CORE_VLC
        } else if (globalCore == Prefs.PLAYER_CORE_AUTO) {
            desiredCore = when (line.streamType.lowercase()) {
                "vlc" -> {
                    coreText = "智能 (VLC)"
                    Prefs.PLAYER_CORE_VLC
                }
                "x5" -> {
                    coreText = "智能 (VLC)"
                    Prefs.PLAYER_CORE_VLC
                }
                "ts", "rtp", "udp" -> {
                    coreText = "智能 (Exo)"
                    Prefs.PLAYER_CORE_EXO
                }
                else -> {
                    coreText = "智能 (Exo)"
                    Prefs.PLAYER_CORE_EXO
                }
            }
        } else {
            coreText = when (desiredCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                else -> "VLC"
            }
        }

        if (coreRetryLevel > 0) {
            coreText = if (coreRetryLevel == 1) "重试 (Exo)" else "重试 (VLC)"
        }
        
        findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvStreamType)?.text = "${line.streamType.uppercase()} ($coreText)"

        // 判断当前已经实例化的 playerHelper 是否与所需的一致
        val isCoreMatch = when (desiredCore) {
            Prefs.PLAYER_CORE_EXO -> playerHelper is com.mediaplayer.app.util.ExoPlayerHelper
            else -> playerHelper is com.mediaplayer.app.util.VlcPlayerHelper
        }

        if (playerHelper == null || !isCoreMatch) {
            playerHelper?.release()
            videoLayout?.removeAllViews() // 清除旧的视图
            initPlayerWithCore(desiredCore)
        }
        retryCount = 0
        progressBuffering?.visibility = View.VISIBLE

        resolveJob?.cancel()
        resolveJob = lifecycleScope.launch {
            val finalUrl = com.mediaplayer.app.util.StreamResolver.resolve(line.streamUrl, line.userAgent, line.customHeaders)
            playerHelper?.play(finalUrl, line.userAgent, line.customHeaders)
        }
        
        // 启动/重置看门狗
        lastPlaybackTime = 0L
        frozenTimeCounter = 0
        uiHandler.removeCallbacks(watchdogRunnable)
        uiHandler.postDelayed(watchdogRunnable, 5000)
        
        // 频道列表中高亮当前播放频道
        channelAdapter.setPlayingIndex(currentChannelIndex)
    }

    private fun showLineSelectionMenu() {
        val channel = allChannels.getOrNull(currentChannelIndex) ?: return
        val lines = channel.getLinesSafely()
        if (lines.size <= 1) {
            Toast.makeText(this, "当前频道只有一条线路", Toast.LENGTH_SHORT).show()
            return
        }
        
        layoutZappingMenu?.visibility = View.GONE
        layoutSettingsMenu?.visibility = View.GONE
        layoutEpgMenu?.visibility = View.GONE
        
        tvLineMenuTitle?.text = "线路"
        containerLines?.removeAllViews()
        
        var firstFocusableView: View? = null
        
        lines.forEachIndexed { index, line ->
            val tv = TextView(this).apply {
                layoutParams = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT,
                    LinearLayout.LayoutParams.WRAP_CONTENT
                ).apply {
                    setMargins(0, 8, 0, 8)
                }
                text = "线路 ${index + 1} (${line.streamType.uppercase()})"
                textSize = 18f
                setPadding(32, 24, 32, 24)
                isFocusable = true
                isClickable = true
                isFocusableInTouchMode = true
                setBackgroundResource(R.drawable.selector_channel_item)
                
                if (index == currentLineIndex) {
                    setTextColor(android.graphics.Color.parseColor("#FFC107"))
                    text = "线路 ${index + 1} (${line.streamType.uppercase()}) - 当前"
                } else {
                    setTextColor(android.graphics.Color.WHITE)
                }
                
                setOnClickListener {
                    if (currentLineIndex != index) {
                        currentLineIndex = index
                        Toast.makeText(this@MainActivity, "已手动切换至线路 ${index + 1}", Toast.LENGTH_SHORT).show()
                        playCurrentLineInTv()
                    }
                    hideLineSelectionMenu()
                }
            }
            containerLines?.addView(tv)
            if (index == currentLineIndex) {
                firstFocusableView = tv
            }
        }
        
        layoutLineMenu?.visibility = View.VISIBLE
        layoutLineMenu?.post {
            val target = firstFocusableView ?: containerLines?.getChildAt(0)
            target?.requestFocus()
        }
    }

    private fun hideLineSelectionMenu() {
        layoutLineMenu?.visibility = View.GONE
        videoLayout?.requestFocus()
    }

    private fun showOsd() {
        layoutOsd?.visibility = View.VISIBLE
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.postDelayed(hideOsdRunnable, 5000)
        
        // 换台时同步触发跑马灯
        if (!sysAnnouncement.isNullOrEmpty() && !marqueeIsVisible) {
            triggerMarquee()
        }
    }
    

    private fun loadEpgForChannel(channel: Channel) {
        if (channel.currentEpg.isNotEmpty()) {
            tvOsdEpg?.text = "正在播放: ${channel.currentEpg}"
            progressEpg?.progress = channel.epgPercent
        } else {
            tvOsdEpg?.text = "暂无当前节目信息"
            progressEpg?.progress = 0
        }
    }

    // ═══════════════════════════════════════════════════
    // PHONE MODE SETUP
    // ═══════════════════════════════════════════════════

    private fun setupPhoneViews() {
        phoneGroupTabs = findViewById(R.id.layoutGroupTabs)
        phoneChannelsRv = findViewById(R.id.rvChannels)
        phoneChannelCount = findViewById(R.id.tvChannelCount)
        phoneAuthWaiting = findViewById(R.id.layoutAuthWaiting)
        phoneContent = findViewById(R.id.layoutContent)
        phoneSearchLayout = findViewById(R.id.layoutSearch)
        phoneSearchEdit = findViewById(R.id.etSearch)
        phoneScrollView = findViewById<View>(R.id.layoutGroupTabs)?.parent as? HorizontalScrollView
        phoneSwipeRefresh = findViewById(R.id.swipeRefresh)
        progressLoading = findViewById(R.id.progressLoading)
        layoutEmpty = findViewById(R.id.layoutEmpty)
        tvEmptyText = findViewById(R.id.tvEmptyText)

        phoneSwipeRefresh?.setColorSchemeResources(R.color.accent)
        phoneSwipeRefresh?.setOnRefreshListener { loadData() }

        findViewById<View>(R.id.btnSearch)?.setOnClickListener {
            toggleSearch()
        }

        findViewById<View>(R.id.btnSettings)?.setOnClickListener {
            startActivity(Intent(this, SettingsActivity::class.java))
        }

        phoneSearchEdit?.addTextChangedListener(object : TextWatcher {
            override fun beforeTextChanged(s: CharSequence?, start: Int, count: Int, after: Int) {}
            override fun onTextChanged(s: CharSequence?, start: Int, before: Int, count: Int) {}
            override fun afterTextChanged(s: Editable?) {
                filterBySearch(s?.toString() ?: "")
            }
        })
    }

    private fun toggleSearch() {
        phoneSearchLayout?.let {
            if (it.visibility == View.VISIBLE) {
                it.visibility = View.GONE
                phoneSearchEdit?.setText("")
                filterChannels()
            } else {
                it.visibility = View.VISIBLE
                phoneSearchEdit?.requestFocus()
            }
        }
    }

    private fun filterBySearch(query: String) {
        if (query.isEmpty()) {
            filterChannels()
            return
        }
        filteredChannels = allChannels.filter {
            it.name.contains(query, ignoreCase = true)
        }
        channelAdapter.submitList(filteredChannels)
        if (!isTvMode) updateChannelCount()
        showEmpty(filteredChannels.isEmpty(), "未找到匹配的频道")
    }

    // ═══════════════════════════════════════════════════
    // SHARED LOGIC
    // ═══════════════════════════════════════════════════

    private fun setupAdapters() {
        groupAdapter = GroupAdapter { group ->
            currentGroupId = group.id
            filterChannels()
            groupAdapter.setSelected(group.id)
            if (!isTvMode) updatePhoneGroupTabs()
        }

        channelAdapter = ChannelAdapter(
            isTvMode = isTvMode,
            onClick = { channel, _ ->
                if (isTvMode) {
                    val realIndex = allChannels.indexOf(channel)
                    playTvChannel(realIndex)
                    uiHandler.postDelayed(hideZappingRunnable, 500)
                } else {
                    currentChannelIndex = allChannels.indexOf(channel)
                    playChannelPhone(channel)
                }
            }
        )

        if (isTvMode) {
            tvGroupsRv?.apply {
                layoutManager = LinearLayoutManager(this@MainActivity)
                adapter = groupAdapter
            }
            tvChannelsRv?.apply {
                layoutManager = LinearLayoutManager(this@MainActivity)
                adapter = channelAdapter
            }
        } else {
            phoneChannelsRv?.apply {
                layoutManager = LinearLayoutManager(this@MainActivity)
                adapter = channelAdapter
            }
        }
    }

    private fun checkAuthAndLoad() {
        lifecycleScope.launch {
            if (authManager.isApproved()) {
                authManager.verify().onSuccess { resp ->
                    if (resp != null) {
                        sysAnnouncement = resp.announcement
                        sysAnnouncementInterval = resp.announcementInterval
                        showContent()
                    } else doRegister()
                }.onFailure { doRegister() }
            } else {
                doRegister()
            }
        }
    }

    private fun doRegister() {
        lifecycleScope.launch {
            showAuthWaiting("正在注册设备...")
            authManager.register().onSuccess { result ->
                when (result.status) {
                    "approved" -> {
                        sysAnnouncement = result.announcement
                        sysAnnouncementInterval = result.announcementInterval
                        showContent()
                    }
                    "pending" -> {
                        showAuthWaiting("设备已注册，等待管理员审批...\n\n设备ID: ${authManager.getDeviceId()}")
                        startAuthPolling()
                    }
                    "rejected" -> showAuthWaiting("设备注册被拒绝\n请联系管理员")
                    "banned" -> showAuthWaiting("设备已被封禁\n请联系管理员")
                }
            }.onFailure { e ->
                // 无论遇到什么错误（DNS解析失败、404、类型转换错误等），都显示配置二维码和地址，防止用户卡死
                val showQr = true
                showAuthWaiting("注册失败: ${e.message}\n\n请检查服务器地址", showQr)
            }
        }
    }

    private fun startAuthPolling() {
        val runnable = object : Runnable {
            override fun run() {
                lifecycleScope.launch {
                    authManager.checkStatus().onSuccess { status ->
                        if (status == "approved") { 
                            authManager.verify().onSuccess { resp ->
                                if (resp != null) {
                                    sysAnnouncement = resp.announcement
                                    sysAnnouncementInterval = resp.announcementInterval
                                }
                                showContent()
                            }
                            return@launch 
                        }
                        authPollRunnable?.let { authPollHandler.postDelayed(it, 10000) }
                    }.onFailure { authPollRunnable?.let { authPollHandler.postDelayed(it, 15000) } }
                }
            }
        }
        authPollRunnable = runnable
        authPollHandler.postDelayed(runnable, 10000)
    }

    private fun showAuthWaiting(message: String, showQr: Boolean = false) {
        if (isTvMode) {
            tvAuthWaiting?.visibility = View.VISIBLE
            findViewById<TextView>(R.id.tvAuthStatus)?.text = message
        } else {
            phoneAuthWaiting?.visibility = View.VISIBLE
            phoneContent?.visibility = View.GONE
            findViewById<TextView>(R.id.tvAuthStatus)?.text = message
        }
        
        if (showQr) {
            val ip = com.mediaplayer.app.util.NetworkUtils.getLocalIpAddress()
            if (ip != null) {
                setupQrConfigServer {
                    Toast.makeText(this@MainActivity, "配置已保存，正在重试...", Toast.LENGTH_LONG).show()
                    checkAuthAndLoad()
                }
                val qrUrl = "http://$ip:9528/"
                val bitmap = com.mediaplayer.app.util.QRCodeHelper.generateQRCode(qrUrl, 400)
                ivAuthQrCode?.setImageBitmap(bitmap)
                tvAuthQrConfigHint?.text = "手机扫码设置服务器\n或访问: $qrUrl"
                tvAuthQrConfigHint?.setOnClickListener {
                    try {
                        val intent = android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(qrUrl))
                        intent.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
                        startActivity(intent)
                    } catch (e: Exception) {
                        e.printStackTrace()
                    }
                }
                layoutAuthQrConfig?.visibility = View.VISIBLE
            }
        } else {
            layoutAuthQrConfig?.visibility = View.GONE
        }
    }

    private fun showContent() {
        if (isTvMode) {
            tvAuthWaiting?.visibility = View.GONE
        } else {
            phoneAuthWaiting?.visibility = View.GONE
            phoneContent?.visibility = View.VISIBLE
        }

        // 初始触发跑马灯
        if (!sysAnnouncement.isNullOrEmpty()) {
            triggerMarquee()
        }

        loadData()
        startHeartbeat()
    }

    private fun triggerMarquee() {
        uiHandler.removeCallbacks(marqueeRunnable)
        uiHandler.removeCallbacks(hideMarqueeRunnable)
        
        val layoutAnnouncement = findViewById<android.view.View>(R.id.layoutAnnouncement) ?: return
        val tvAnnouncement = findViewById<android.widget.TextView>(R.id.tvAnnouncement) ?: return
        
        if (sysAnnouncement.isNullOrEmpty()) {
            layoutAnnouncement.visibility = View.GONE
            marqueeIsVisible = false
            return
        }

        layoutAnnouncement.visibility = View.VISIBLE
        marqueeIsVisible = true
        
        // 强制原生跑马灯滚动：如果文字太短，补齐空格直到超过屏幕宽度
        var text = sysAnnouncement!!
        tvAnnouncement.text = text
        
        layoutAnnouncement.post {
            tvAnnouncement.measure(0, 0)
            val screenWidth = layoutAnnouncement.width
            val textWidth = tvAnnouncement.measuredWidth
            
            if (textWidth < screenWidth && textWidth > 0) {
                val spaceWidth = tvAnnouncement.paint.measureText(" ")
                if (spaceWidth > 0) {
                    val spacesNeeded = ((screenWidth - textWidth) / spaceWidth).toInt() + 10
                    text = text + " ".repeat(spacesNeeded) + text
                    tvAnnouncement.text = text
                }
            }
            tvAnnouncement.isSelected = true
            
            // 计算原生跑马灯需要多长时间才能跑完一遍
            // Android 底层源码设定的跑马灯速度固定为 30dp/秒
            val density = resources.displayMetrics.density
            val speedPxPerSec = 30f * density
            // 跑完一整圈需要走过的总距离 = 屏幕宽度 + 文字总长度
            val requiredTimeMs = ((screenWidth + textWidth) / speedPxPerSec * 1000f).toLong()
            
            // 取 25 秒和实际需要时间中的最大值，确保哪怕是超长文本也能至少被完整看完一遍
            val displayDuration = maxOf(25000L, requiredTimeMs)
            
            // 原生跑马灯运行完毕后自动隐藏，并进入下一轮间隔排期
            uiHandler.postDelayed(hideMarqueeRunnable, displayDuration)
        }
    }

    private fun startHeartbeat() {
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        val runnable = object : Runnable {
            override fun run() {
                lifecycleScope.launch {
                    try {
                        authManager.verify()
                    } catch (_: Exception) {}
                }
                heartbeatHandler.postDelayed(this, 3 * 60 * 1000) // 每3分钟心跳
            }
        }
        heartbeatRunnable = runnable
        heartbeatHandler.postDelayed(runnable, 3 * 60 * 1000)
    }

    private fun loadData() {
        if (!isTvMode) showLoading(true)
        lifecycleScope.launch {
            // 1. 先拉分组列表
            val realGroups = repo.getGroups().getOrElse { emptyList() }
            groups = listOf(ChannelGroup(id = 0, name = "全部")) + realGroups
            groupAdapter.submitList(groups)
            groupAdapter.setSelected(0)
            if (!isTvMode) buildPhoneGroupTabs()

            // 2. 按分组并行拉取全量频道（彻底绕过全局 page_size 上限）
            repo.getAllChannelsByGroups(realGroups).onSuccess { list ->
                list.forEachIndexed { index, channel ->
                    channel.globalIndex = index
                }
                allChannels = list
                filteredChannels = list
                channelAdapter.submitList(list)
                if (!isTvMode) updateChannelCount()
                if (!isTvMode) showEmpty(list.isEmpty())

                if (list.isNotEmpty()) {
                    if (isTvMode) {
                        // 尝试恢复上次播放的频道
                        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
                        val lastChannelId = prefs.getLong("last_channel_id", -1L)
                        var targetIndex = 0
                        if (lastChannelId != -1L) {
                            val foundIndex = list.indexOfFirst { it.id == lastChannelId }
                            if (foundIndex != -1) {
                                targetIndex = foundIndex
                            }
                        }
                        playTvChannel(targetIndex)
                        videoLayout?.requestFocus()
                    } else {
                        currentChannelIndex = 0
                    }
                }
            }.onFailure {
                if (!isTvMode) showEmpty(true, "加载失败，请检查网络")
            }
            if (!isTvMode) showLoading(false)
            phoneSwipeRefresh?.isRefreshing = false
        }
    }


    private fun showLoading(show: Boolean) {
        progressLoading?.visibility = if (show) View.VISIBLE else View.GONE
    }

    private fun showEmpty(show: Boolean, message: String = getString(R.string.no_channels)) {
        layoutEmpty?.visibility = if (show) View.VISIBLE else View.GONE
        tvEmptyText?.text = message
    }

    private fun filterChannels() {
        filteredChannels = if (currentGroupId == 0L) {
            allChannels
        } else {
            allChannels.filter { it.groupId == currentGroupId }
        }
        channelAdapter.submitList(filteredChannels)
        tvChannelsRv?.scrollToPosition(0) // 分组切换时，频道列表重置到顶部
        if (!isTvMode) updateChannelCount()
        if (!isTvMode) showEmpty(filteredChannels.isEmpty(), "该分组暂无频道")
    }

    private fun updateChannelCount() {
        val text = "${filteredChannels.size} 个频道"
        phoneChannelCount?.text = text
    }

    // ── Phone group tabs ───────────────────────────────

    private fun buildPhoneGroupTabs() {
        phoneGroupTabs?.removeAllViews()
        groups.forEach { group ->
            val tab = LayoutInflater.from(this).inflate(R.layout.item_group_tab, phoneGroupTabs, false) as TextView
            tab.text = group.name
            tab.isSelected = group.id == currentGroupId
            tab.setOnClickListener {
                currentGroupId = group.id
                filterChannels()
                updatePhoneGroupTabs()
            }
            phoneGroupTabs?.addView(tab)
        }
    }

    private fun updatePhoneGroupTabs() {
        for (i in 0 until (phoneGroupTabs?.childCount ?: 0)) {
            phoneGroupTabs?.getChildAt(i)?.isSelected = groups.getOrNull(i)?.id == currentGroupId
        }
    }

    // ── Play channel (Phone) ───────────────────────────

    private fun playChannelPhone(channel: Channel) {
        val lines = channel.getLinesSafely()
        val firstLine = if (lines.isNotEmpty()) lines[0] else ChannelLine()
        
        val intent = Intent(this, PlayerActivity::class.java).apply {
            putExtra("channel_id", channel.id)
            putExtra("channel_name", channel.name)
            putExtra("stream_url", firstLine.streamUrl)
            putExtra("stream_type", firstLine.streamType)
            putExtra("channel_index", currentChannelIndex)
            putExtra("user_agent", firstLine.userAgent)
            putExtra("custom_headers", firstLine.customHeaders)
        }
        startActivity(intent)
    }

    private fun showEpgMenu() {
        if (currentChannelIndex < 0 || currentChannelIndex >= allChannels.size) return
        val channel = allChannels[currentChannelIndex]
        
        layoutZappingMenu?.visibility = View.GONE
        layoutSettingsMenu?.visibility = View.GONE
        layoutEpgMenu?.visibility = View.VISIBLE
        
        tvEpgMenuTitle?.text = "节目单"
        
        val cached = com.mediaplayer.app.util.EpgCacheManager.get(channel.name)
        if (cached != null) {
            progressEpgLoading?.visibility = View.GONE
            if (cached.isEmpty()) {
                tvEpgEmptyText?.visibility = View.VISIBLE
                rvEpgList?.visibility = View.GONE
            } else {
                tvEpgEmptyText?.visibility = View.GONE
                rvEpgList?.visibility = View.VISIBLE
                epgAdapter.setSupportCatchup(channel.supportCatchup)
                if (currentChannelIndex == currentCatchupChannelIndex) {
                    epgAdapter.setActiveProgramStartTime(currentCatchupStartTime)
                } else {
                    epgAdapter.setActiveProgramStartTime(null)
                }
                epgAdapter.setData(cached)
                val pIndex = epgAdapter.getPlayingIndex()
                if (pIndex >= 0) {
                    rvEpgList?.scrollToPosition(pIndex)
                    rvEpgList?.post {
                        rvEpgList?.layoutManager?.findViewByPosition(pIndex)?.requestFocus()
                    }
                } else {
                    rvEpgList?.requestFocus()
                }
            }
            return
        }

        rvEpgList?.visibility = View.GONE
        tvEpgEmptyText?.visibility = View.GONE
        progressEpgLoading?.visibility = View.VISIBLE
        rvEpgList?.requestFocus()

        lifecycleScope.launch(Dispatchers.IO) {
            try {
                // Backend uses channel_id as string, pass channel.name for fuzzy match
                val epgId = channel.name
                val response = ApiClient.getService().getEPG(epgId)
                withContext(Dispatchers.Main) {
                    progressEpgLoading?.visibility = View.GONE
                    if (response.isSuccessful && response.body()?.code == 0) {
                        val programs = response.body()?.data ?: emptyList()
                        com.mediaplayer.app.util.EpgCacheManager.put(channel.name, programs)
                        if (programs.isEmpty()) {
                            tvEpgEmptyText?.visibility = View.VISIBLE
                        } else {
                            rvEpgList?.visibility = View.VISIBLE
                            epgAdapter.setSupportCatchup(channel.supportCatchup)
                            if (currentChannelIndex == currentCatchupChannelIndex) {
                                epgAdapter.setActiveProgramStartTime(currentCatchupStartTime)
                            } else {
                                epgAdapter.setActiveProgramStartTime(null)
                            }
                            epgAdapter.setData(programs)
                            
                            // Scroll to playing index
                            val pIndex = epgAdapter.getPlayingIndex()
                            if (pIndex >= 0) {
                                rvEpgList?.scrollToPosition(pIndex)
                                rvEpgList?.post {
                                    rvEpgList?.layoutManager?.findViewByPosition(pIndex)?.requestFocus()
                                }
                            }
                        }
                    } else {
                        tvEpgEmptyText?.visibility = View.VISIBLE
                    }
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    progressEpgLoading?.visibility = View.GONE
                    tvEpgEmptyText?.visibility = View.VISIBLE
                }
            }
        }
    }

    private fun hideEpgMenu() {
        layoutEpgMenu?.visibility = View.GONE
        videoLayout?.requestFocus()
    }

    // ── Number Input State ──
    private var channelInputBuffer = java.lang.StringBuilder()
    private var isInputtingChannel = false
    private val channelInputRunnable = Runnable {
        isInputtingChannel = false
        val inputNum = channelInputBuffer.toString().toIntOrNull()
        channelInputBuffer.clear()
        
        if (inputNum != null && inputNum > 0 && allChannels.isNotEmpty()) {
            val targetIndex = allChannels.indexOfFirst { it.globalIndex + 1 == inputNum }
            if (targetIndex != -1) {
                playTvChannel(targetIndex)
            } else {
                Toast.makeText(this@MainActivity, "未找到频道: $inputNum", Toast.LENGTH_SHORT).show()
                // Restore OSD to current playing channel info
                if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) {
                    val currentChannel = allChannels[currentChannelIndex]
                    tvOsdChannelNum?.text = String.format("%03d", currentChannel.globalIndex + 1)
                    tvOsdChannelName?.text = currentChannel.name
                }
            }
        } else {
             // Restore OSD
             if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) {
                 val currentChannel = allChannels[currentChannelIndex]
                 tvOsdChannelNum?.text = String.format("%03d", currentChannel.globalIndex + 1)
                 tvOsdChannelName?.text = currentChannel.name
             }
        }
    }

    override fun dispatchTouchEvent(ev: android.view.MotionEvent): Boolean {
        if (ev.action == android.view.MotionEvent.ACTION_DOWN || ev.action == android.view.MotionEvent.ACTION_MOVE) {
            if (layoutZappingMenu?.visibility == View.VISIBLE) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 15000)
            }
        }
        return super.dispatchTouchEvent(ev)
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            if (layoutZappingMenu?.visibility == View.VISIBLE) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 15000)
            }
            
            // 遥控器数字键换台
            if (isTvMode && tvAuthWaiting?.visibility == View.GONE) {
                val keyCode = event.keyCode
                if (keyCode in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 || keyCode in KeyEvent.KEYCODE_NUMPAD_0..KeyEvent.KEYCODE_NUMPAD_9) {
                    val digit = if (keyCode >= KeyEvent.KEYCODE_NUMPAD_0) {
                        keyCode - KeyEvent.KEYCODE_NUMPAD_0
                    } else {
                        keyCode - KeyEvent.KEYCODE_0
                    }
                    
                    isInputtingChannel = true
                    if (channelInputBuffer.length < 4) {
                        channelInputBuffer.append(digit)
                    }
                    
                    showOsd()
                    tvOsdChannelNum?.text = channelInputBuffer.toString()
                    tvOsdChannelName?.text = "输入频道号..."
                    
                    uiHandler.removeCallbacks(channelInputRunnable)
                    uiHandler.postDelayed(channelInputRunnable, 1500)
                    return true
                }
            }
        }
        return super.dispatchKeyEvent(event)
    }

    // ── TV key events ──────────────────────────────────

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        // 任何时候按下菜单键，直接显示右侧设置
        if (keyCode == KeyEvent.KEYCODE_MENU) {
            val isSettingsVisible = layoutSettingsMenu?.visibility == View.VISIBLE
            if (isSettingsVisible) hideSettingsMenu() else showSettingsMenu()
            return true
        }

        if (isTvMode && tvAuthWaiting?.visibility == View.GONE) {
            val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
            val isSettingsVisible = layoutSettingsMenu?.visibility == View.VISIBLE
            val isEpgVisible = layoutEpgMenu?.visibility == View.VISIBLE
            val isLineVisible = layoutLineMenu?.visibility == View.VISIBLE

            val anyPanelOpen = isMenuVisible || isSettingsVisible || isEpgVisible || isLineVisible

            // 当任何面板未显示时，开始追踪 OK 键的长按事件
            if (!anyPanelOpen && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                event?.startTracking()
                return true
            }

            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> {
                    if (!anyPanelOpen) {
                        val prev = if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        playTvChannel(prev)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (!anyPanelOpen) {
                        val next = if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        playTvChannel(next)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_LEFT -> {
                    if (isSettingsVisible) {
                        val focus = currentFocus
                        if (focus is android.widget.SeekBar || focus is android.widget.EditText) {
                            return false
                        }

                        hideSettingsMenu()
                        return true
                    }
                    if (isEpgVisible) {
                        hideEpgMenu()
                        return true
                    }
                    if (isLineVisible) {
                        hideLineSelectionMenu()
                        return true
                    }
                    if (!isMenuVisible) {
                        layoutZappingMenu?.visibility = View.VISIBLE
                        tvGroupsRv?.requestFocus()
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        uiHandler.postDelayed(hideZappingRunnable, 10000)
                        return true
                    }
                    // 如果 isMenuVisible 为 true，不拦截，让焦点能在菜单内部向左移动（从频道到分组）
                }
                KeyEvent.KEYCODE_DPAD_RIGHT -> {
                    if (isSettingsVisible) {
                        val focus = currentFocus
                        if (focus is android.widget.SeekBar || focus is android.widget.EditText) {
                            return false
                        }

                        return true
                    }
                    if (isEpgVisible) {
                        return true
                    }
                    if (isLineVisible) {
                        hideLineSelectionMenu()
                        return true
                    }
                    if (isMenuVisible) {
                        if (tvChannelsRv?.hasFocus() == true) {
                            // 如果已经在频道列表（最右侧），再按右键则关闭菜单
                            uiHandler.removeCallbacks(hideZappingRunnable)
                            hideZappingRunnable.run()
                            return true
                        }
                        // 如果焦点在分组列表，不拦截，让焦点能向右移动到频道列表
                    } else {
                        // 如果菜单未显示，按右键呼出完整 EPG 节目单
                        showEpgMenu()
                        return true
                    }
                }
                KeyEvent.KEYCODE_BACK -> {
                    if (layoutLineMenu?.visibility == View.VISIBLE) {
                        hideLineSelectionMenu()
                        return true
                    } else if (layoutEpgMenu?.visibility == View.VISIBLE) {
                        hideEpgMenu()
                        return true
                    } else if (isSettingsVisible) {
                        hideSettingsMenu()
                        return true
                    } else if (isMenuVisible) {
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        hideZappingRunnable.run()
                        return true
                    } else {
                        // 退出确认或者直接退出
                        finish()
                        return true
                    }
                }
            }
            if (isMenuVisible && keyCode != KeyEvent.KEYCODE_DPAD_CENTER && keyCode != KeyEvent.KEYCODE_ENTER) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 10000)
            }
        }
        return super.onKeyDown(keyCode, event)
    }

    override fun onKeyLongPress(keyCode: Int, event: KeyEvent?): Boolean {
        if (isTvMode && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
            // 长按 OK 键呼出手动切源菜单
            showLineSelectionMenu()
            return true
        }
        return super.onKeyLongPress(keyCode, event)
    }

    override fun onKeyUp(keyCode: Int, event: KeyEvent?): Boolean {
        if (isTvMode && tvAuthWaiting?.visibility == View.GONE) {
            val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
            val isSettingsVisible = layoutSettingsMenu?.visibility == View.VISIBLE
            
            if (!isMenuVisible && !isSettingsVisible && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                if (event?.isTracking == true && !event.isCanceled) {
                    // 短按 OK 键，呼出频道列表
                    layoutZappingMenu?.visibility = View.VISIBLE
                    val playingId = if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) allChannels[currentChannelIndex].id else -1L
                    val indexInFiltered = filteredChannels.indexOfFirst { it.id == playingId }
                    if (indexInFiltered >= 0) {
                        tvChannelsRv?.scrollToPosition(indexInFiltered)
                        tvChannelsRv?.postDelayed({
                            val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                            lm?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                        }, 50)
                    } else {
                        tvChannelsRv?.requestFocus()
                    }
                    uiHandler.removeCallbacks(hideZappingRunnable)
                    uiHandler.postDelayed(hideZappingRunnable, 10000)
                }
                return true
            }
        }
        return super.onKeyUp(keyCode, event)
    }

    companion object {
        @Volatile
        @JvmStatic
        var settingsChanged = false
    }

    override fun onResume() {
        super.onResume()
        hideSystemUI()
        if (settingsChanged || allChannels.isEmpty()) {
            loadData()
            settingsChanged = false
        } else if (isTvMode && allChannels.isNotEmpty() && currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) {
            // 直播流在切后台后会断开或缓冲失效，必须重新连接加载
            videoLayout?.post {
                playTvChannel(currentChannelIndex)
            }
        }
    }
    
    override fun onPause() {
        super.onPause()
        uiHandler.removeCallbacks(watchdogRunnable)
        if (isTvMode) {
            // 直播流切后台直接彻底停止，释放硬件解码器和网络连接
            playerHelper?.release()
            playerHelper = null
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        configWebServer?.stop()
        authPollRunnable?.let { authPollHandler.removeCallbacks(it) }
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.removeCallbacks(hideZappingRunnable)
        uiHandler.removeCallbacks(channelInputRunnable)
        uiHandler.removeCallbacks(watchdogRunnable)
        playerHelper?.release()
        playerHelper = null
    }
}
