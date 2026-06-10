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
import android.view.WindowManager
import androidx.appcompat.app.AppCompatActivity
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.lifecycleScope
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


    // ── Shared loading/empty views ──
    private var progressLoading: ProgressBar? = null

    // ── Catchup State ──
    private var currentCatchupStartTime: String? = null
    private var currentCatchupChannelIndex: Int = -1

    // ── Data ──
    private var groups = listOf<ChannelGroup>()
    private var allChannels = listOf<Channel>()
    private var channelsByGroup: Map<Long, List<Channel>> = emptyMap()
    private var filteredChannels = listOf<Channel>()
    private var currentGroupId = 0L
    private var currentChannelIndex = 0
    private var currentLineIndex = 0

    private lateinit var groupAdapter: GroupAdapter
    private lateinit var channelAdapter: ChannelAdapter

    private val authPollHandler = Handler(Looper.getMainLooper())
    private var authPollRunnable: Runnable? = null
    private val heartbeatHandler = Handler(Looper.getMainLooper())
    private var heartbeatRunnable: Runnable? = null
    private val focusDebounceHandler = Handler(Looper.getMainLooper())
    private var focusDebounceRunnable: Runnable? = null

    private var playerHelper: com.mediaplayer.app.util.IPlayerHelper? = null
    private var continuousSkipCount = 0
    private val maxAutoSkips = 5
    private val uiHandler = Handler(Looper.getMainLooper())
    private var coreRetryLevel = 0
    private var isWatchdogEnabledForCurrentStream = false
    
    // 数据加载并发锁
    private var isLoadingData = false
    // 首次 onResume 标记（防止与 onCreate 的认证链路冲突）
    private var isFirstResume = true
    
    // Watchdog State
    enum class PlaybackState { IDLE, BUFFERING, PLAYING }
    private var currentPlaybackState = PlaybackState.IDLE
    private var stateStartTime = 0L
    private var lastPlaybackTime = 0L
    private var frozenTimeCounter = 0
    private val watchdogRunnable = object : Runnable {
        override fun run() {
            if (isTvMode && playerHelper != null) {
                val now = System.currentTimeMillis()
                when (currentPlaybackState) {
                    PlaybackState.BUFFERING -> {
                        // 场景 A：连接或缓冲超时（10秒未进入 PLAYING）
                        // 注释掉该看门狗策略，将超时判断权完全交还给底层播放器（ExoPlayer/VLC）。
                        // 避免在弱网或加载较慢的有效源上出现频繁的“误杀”和自动换台跳跃。
                        /*
                        if (stateStartTime > 0 && now - stateStartTime > 10000L) {
                            Toast.makeText(this@MainActivity, "网络连接超时，正在尝试切换线路...", Toast.LENGTH_SHORT).show()
                            currentPlaybackState = PlaybackState.IDLE
                            handlePlaybackError(isNetworkTimeout = true)
                            return
                        }
                        */
                    }
                    PlaybackState.PLAYING -> {
                        // 场景 B：画面冻结（假死）逻辑
                        // 由于多数直播流底层时间戳不会随播放推进而正常改变（getTime() 返回固定值），
                        // 这里通过比对 currentTime == lastPlaybackTime 极易造成正常播放时的误判卡死，
                        // 因此将此逻辑注释掉。如有真实卡顿，应依靠播放器的 onBuffering 或 onError 来处理。
                        /*
                        if (!isWatchdogEnabledForCurrentStream) {
                            lastPlaybackTime = playerHelper?.getTime() ?: 0L
                            frozenTimeCounter = 0
                        } else {
                            val currentTime = playerHelper?.getTime() ?: 0L
                            if (currentTime > 0 && currentTime == lastPlaybackTime) {
                                frozenTimeCounter++
                                if (frozenTimeCounter >= 4) { // 4 * 2s = 8s
                                    Toast.makeText(this@MainActivity, "检测到画面卡死，正在尝试恢复...", Toast.LENGTH_SHORT).show()
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
                        */
                    }
                    PlaybackState.IDLE -> {
                        frozenTimeCounter = 0
                    }
                }
            }
            uiHandler.postDelayed(this, 2000)
        }
    }

    private val hideOsdRunnable = Runnable { 
        layoutOsd?.visibility = View.GONE 
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "OSD GONE")
    }
    private val hideZappingRunnable = Runnable { 
        layoutZappingMenu?.visibility = View.GONE
        activeListArea = "channels"
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "ZappingMenu GONE")
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

    private var activeListArea = "channels" // "groups", "channels", "epg"

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        super.onCreate(savedInstanceState)
        
        // 终极全局焦点防溢出守护神：监听由系统(如RecyclerView回收)引发的非自愿焦点跳转
        window.decorView.viewTreeObserver.addOnGlobalFocusChangeListener { oldFocus, newFocus ->
            if (newFocus != null) {
                val isNewGroups = isViewDescendantOf(newFocus, tvGroupsRv)
                val isNewChannels = isViewDescendantOf(newFocus, tvChannelsRv)
                val isNewEpg = isViewDescendantOf(newFocus, rvEpgList)

                if (isNewGroups && activeListArea != "groups") {
                    com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "Involuntary jump to Groups rejected! Forcing back to $activeListArea.")
                    bounceFocusBack()
                } else if (isNewChannels && activeListArea != "channels") {
                    com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "Involuntary jump to Channels rejected! Forcing back to $activeListArea.")
                    bounceFocusBack()
                } else if (isNewEpg && activeListArea != "epg") {
                    com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "Involuntary jump to EPG rejected! Forcing back to $activeListArea.")
                    bounceFocusBack()
                }
            }
        }

        // 强制所有设备使用 TV 的沉浸式界面大一统！
        isTvMode = true
        
        // 安全地请求横屏方向，规避 Android 8.0 透明主题请求固定方向时的崩溃Bug
        try {
            requestedOrientation = android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
        } catch (e: Exception) {
            // 忽略 Android 8.0 上的 IllegalStateException
        }

        // 保持屏幕常亮，防止手机/Pad自动锁屏
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        // 兼容刘海屏/挖孔屏/灵动岛：允许画面延伸到全部屏幕边缘
        // Android 15+ (API 35): ALWAYS 模式确保长边（灵动岛）区域也被覆盖
        // Android 9-14 (API 28-34): SHORT_EDGES 已足够覆盖所有刘海/挖孔场景
        if (android.os.Build.VERSION.SDK_INT >= android.os.Build.VERSION_CODES.P) {
            val lp = window.attributes
            lp.layoutInDisplayCutoutMode = if (android.os.Build.VERSION.SDK_INT >= 35) {
                // LAYOUT_IN_DISPLAY_CUTOUT_MODE_ALWAYS = 3 (Android 15+)
                3
            } else {
                WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES
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
                // 单点：显示 OSD（5s 自动隐藏）
                showOsd()
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
                // 左右滑动：上下文感知的抽屉式交互
                else if (kotlin.math.abs(deltaX) > kotlin.math.abs(deltaY) && kotlin.math.abs(deltaX) > 100) {
                    if (deltaX < 0) {
                        // 向左滑动：侧边栏已显示则关闭，否则显示 EPG
                        if (layoutZappingMenu?.visibility == View.VISIBLE) {
                            uiHandler.removeCallbacks(hideZappingRunnable)
                            hideZappingRunnable.run()
                            return true
                        } else if (layoutEpgMenu?.visibility != View.VISIBLE && layoutSettingsMenu?.visibility != View.VISIBLE) {
                            showEpgMenu()
                            return true
                        }
                    } else {
                        // 向右滑动：EPG 已显示则关闭，否则显示侧边栏
                        if (layoutEpgMenu?.visibility == View.VISIBLE) {
                            hideEpgMenu()
                            return true
                        } else if (layoutZappingMenu?.visibility != View.VISIBLE && layoutSettingsMenu?.visibility != View.VISIBLE) {
                            showZappingMenu(focusOnGroups = false, resetToPlaying = true)
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
        rvEpgList?.layoutManager = object : androidx.recyclerview.widget.LinearLayoutManager(this) {
            override fun onFocusSearchFailed(focused: View, focusDirection: Int, recycler: androidx.recyclerview.widget.RecyclerView.Recycler, state: androidx.recyclerview.widget.RecyclerView.State): View? {
                val next = super.onFocusSearchFailed(focused, focusDirection, recycler, state)
                if (next == null && (focusDirection == View.FOCUS_DOWN || focusDirection == View.FOCUS_UP)) {
                    return focused // 捕获焦点
                }
                return next
            }
            override fun requestChildRectangleOnScreen(parent: androidx.recyclerview.widget.RecyclerView, child: View, rect: android.graphics.Rect, immediate: Boolean, focusedChildVisible: Boolean): Boolean {
                // 核心UX优化：预留上下各 2 个 Item 的高度作为焦点边距
                // 这样在焦点达到倒数第 3 个时，列表就会提前向上滚动
                rect.top -= child.height * 2
                rect.bottom += child.height * 2
                return super.requestChildRectangleOnScreen(parent, child, rect, immediate, focusedChildVisible)
            }
        }
        rvEpgList?.setOnKeyListener { _, keyCode, event ->
            if (event.action == android.view.KeyEvent.ACTION_DOWN) {
                if (FocusHelper.trapVerticalScroll(rvEpgList!!, keyCode)) return@setOnKeyListener true
            }
            false
        }
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
            FocusHelper.linkHorizontalFocus(groupsRv, channelsRv) {
                val groupIndex = groupAdapter.currentList.indexOfFirst { it.id == currentGroupId }
                if (groupIndex >= 0) {
                    val lm = groupsRv.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                    lm?.findViewByPosition(groupIndex)?.requestFocus() ?: groupsRv.requestFocus()
                    true
                } else {
                    false
                }
            }
        }
    }

    private var btnSettingsScale: View? = null
    private var btnSettingsDecoder: View? = null
    private var btnSettingsCore: View? = null
    private var currentCore = Prefs.PLAYER_CORE_AUTO

    private fun setupSettingsViews() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val url = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL)
        
        btnSettingsDecoder = findViewById(R.id.btnSettingsDecoder)
        btnSettingsCore = findViewById(R.id.btnSettingsCore)
        btnSettingsScale = findViewById(R.id.btnSettingsScale)
        val btnSettingsAutoStart = findViewById<View>(R.id.btnSettingsAutoStart)
        val btnSettingsReverseChannels = findViewById<View>(R.id.btnSettingsReverseChannels)
        
        fun updateDecoderText(mode: Int) {
            findViewById<TextView>(R.id.tvSettingsDecoderValue)?.text = when (mode) {
                Prefs.DECODER_MODE_HARDWARE -> "强制硬解"
                Prefs.DECODER_MODE_SOFTWARE -> "强制软解"
                else -> "自动识别"
            }
        }

        fun updateScaleText(mode: Int) {
            findViewById<TextView>(R.id.tvSettingsScaleValue)?.text = when (mode) {
                Prefs.SCALE_MODE_STRETCH -> "强制 16:9"
                Prefs.SCALE_MODE_4_3 -> "强制 4:3"
                else -> "原始比例"
            }
        }

        fun updateAutoStartText(enabled: Boolean) {
            findViewById<TextView>(R.id.tvSettingsAutoStartValue)?.text = if (enabled) "开" else "关"
        }
        
        fun updateReverseChannelsText(enabled: Boolean) {
            findViewById<TextView>(R.id.tvSettingsReverseChannelsValue)?.text = if (enabled) "开" else "关"
        }
        
        var currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        currentCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        // 迁移旧版本中 X5 内核的选择（X5 已移除，自动回退到智能切换）
        if (currentCore == 4) {
            currentCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, currentCore).apply()
        }
        var currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        var currentAutoStart = prefs.getBoolean(Prefs.KEY_AUTO_START, true)
        var currentShowLogo = prefs.getBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, true)
        var currentReverseChannels = prefs.getBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, false)

        updateDecoderText(currentDecoderMode)
        updateCoreText(currentCore)
        updateScaleText(currentScaleMode)
        updateAutoStartText(currentAutoStart)
        updateShowLogoText(currentShowLogo)
        updateReverseChannelsText(currentReverseChannels)
        
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
                Prefs.PLAYER_CORE_VLC -> Prefs.PLAYER_CORE_IJK
                else -> Prefs.PLAYER_CORE_AUTO
            }
            updateCoreText(currentCore)
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, currentCore).apply()
            Toast.makeText(this, "播放内核已保存，下次播放生效", Toast.LENGTH_SHORT).show()
        }
        
        val btnSettingsShowLogo = findViewById<View>(R.id.btnSettingsShowLogo)
        btnSettingsShowLogo?.setOnClickListener {
            currentShowLogo = !currentShowLogo
            updateShowLogoText(currentShowLogo)
            prefs.edit().putBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, currentShowLogo).apply()
            
            // 立即生效
            if (::channelAdapter.isInitialized) {
                channelAdapter.showLogo = currentShowLogo
                channelAdapter.notifyDataSetChanged()
            }
        }
        
        btnSettingsScale?.setOnClickListener {
            // 循环：原始比例 → 强制16:9 → 强制4:3 → 原始比例（已移除放大裁剪）
            currentScaleMode = when (currentScaleMode) {
                Prefs.SCALE_MODE_DEFAULT -> Prefs.SCALE_MODE_STRETCH
                Prefs.SCALE_MODE_STRETCH -> Prefs.SCALE_MODE_4_3
                else -> Prefs.SCALE_MODE_DEFAULT
            }
            updateScaleText(currentScaleMode)
            prefs.edit().putInt(Prefs.KEY_SCALE_MODE, currentScaleMode).apply()
            
            // 立即生效
            when (currentScaleMode) {
                Prefs.SCALE_MODE_STRETCH -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_STRETCH)
                Prefs.SCALE_MODE_4_3 -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_4_3)
                else -> playerHelper?.setAspectRatio(Prefs.SCALE_MODE_DEFAULT)
            }
        }

        btnSettingsAutoStart?.setOnClickListener {
            currentAutoStart = !currentAutoStart
            updateAutoStartText(currentAutoStart)
            prefs.edit().putBoolean(Prefs.KEY_AUTO_START, currentAutoStart).apply()
        }

        btnSettingsReverseChannels?.setOnClickListener {
            currentReverseChannels = !currentReverseChannels
            updateReverseChannelsText(currentReverseChannels)
            prefs.edit().putBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, currentReverseChannels).apply()
        }

        val btnSettingsCheckUpdate = findViewById<View>(R.id.btnSettingsCheckUpdate)
        btnSettingsCheckUpdate?.setOnClickListener {
            com.mediaplayer.app.util.UpdateManager.checkUpdate(this, lifecycleScope, true)
        }

        etSettingsUrl?.setText(url)
        
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        val progress = if (cacheMs == 0) 0 else (cacheMs / 50).coerceIn(1, 100)
        sbSettingsCache?.progress = progress
        tvSettingsCacheValue?.text = if (cacheMs == 0) " 自动" else " ${"%.2f".format(cacheMs / 1000f)} 秒"

        sbSettingsCache?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                val newCacheMs = if (progress == 0) 0 else progress * 50
                tvSettingsCacheValue?.text = if (newCacheMs == 0) " 自动" else " ${"%.2f".format(newCacheMs / 1000f)} 秒"
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {
                val p = seekBar?.progress ?: 0
                val newCacheMs = if (p == 0) 0 else p * 50
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
        
        findViewById<View>(R.id.btnSettingsScale)?.requestFocus()
    }

    private fun initPlayerWithCore(core: Int) {
        val listener = object : com.mediaplayer.app.util.IPlayerHelper.PlayerListener {
            override fun onBuffering(percent: Float) {
                uiHandler.post {
                    if (currentPlaybackState != PlaybackState.BUFFERING) {
                        currentPlaybackState = PlaybackState.BUFFERING
                        stateStartTime = System.currentTimeMillis()
                    }
                    if (percent >= 100f || percent == 0f) {
                        progressBuffering?.visibility = View.GONE
                    } else {
                        progressBuffering?.visibility = View.VISIBLE
                    }
                }
            }
            override fun onPlaying(resolution: String) {
                uiHandler.post {
                    currentPlaybackState = PlaybackState.PLAYING
                    stateStartTime = System.currentTimeMillis()
                    progressBuffering?.visibility = View.GONE
                    continuousSkipCount = 0
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
                            Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
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
                        com.mediaplayer.app.util.RemoteLogger.i("Player", "Playback started successfully. Stream info: $fullInfo")
                    }
                }
            }
            override fun onError() {
                uiHandler.post { 
                    currentPlaybackState = PlaybackState.IDLE
                    handlePlaybackError(isNetworkTimeout = false) 
                }
            }
        }

        try {
            when (core) {
                Prefs.PLAYER_CORE_EXO -> {
                    playerHelper = com.mediaplayer.app.util.ExoPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
                }
                Prefs.PLAYER_CORE_IJK -> {
                    playerHelper = com.mediaplayer.app.util.IjkPlayerHelper(this, videoLayout as android.view.ViewGroup, listener)
                }
                else -> {
                    val vlcVideoLayout = org.videolan.libvlc.util.VLCVideoLayout(this)
                    vlcVideoLayout.layoutParams = android.widget.FrameLayout.LayoutParams(android.widget.FrameLayout.LayoutParams.MATCH_PARENT, android.widget.FrameLayout.LayoutParams.MATCH_PARENT)
                    videoLayout?.addView(vlcVideoLayout)
                    
                    playerHelper = com.mediaplayer.app.util.VlcPlayerHelper(this, vlcVideoLayout, listener)
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
            Toast.makeText(this, "播放内核初始化失败: ${e.message}", Toast.LENGTH_LONG).show()
            // 回退到默认状态或触发错误逻辑
            listener.onError()
        }

        // 创建播放器后，应用保存的画面比例设置
        val scalePrefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val savedScaleMode = scalePrefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        playerHelper?.setAspectRatio(savedScaleMode)
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
        activeListArea = "channels"
    }

    private fun updateCoreText(core: Int) {
        findViewById<TextView>(R.id.tvSettingsCoreValue)?.text = when (core) {
            Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
            Prefs.PLAYER_CORE_VLC -> "VLC"
            Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
            else -> "智能切换"
        }
    }

    private fun updateShowLogoText(show: Boolean) {
        findViewById<TextView>(R.id.tvSettingsShowLogoValue)?.text = if (show) "显示" else "隐藏"
    }

    private fun handlePlaybackError(isNetworkTimeout: Boolean = false) {
        currentPlaybackState = PlaybackState.IDLE
        progressBuffering?.visibility = View.GONE

        // ===== Catchup 回看流自然结束或出错，不触发自动换源 =====
        if (currentCatchupStartTime != null) {
            tvOsdInfo?.text = "回看播放完毕"
            showOsd()
            currentCatchupStartTime = null
            currentCatchupChannelIndex = -1
            val channel = allChannels.getOrNull(currentChannelIndex)
            if (channel != null) loadEpgForChannel(channel)
            com.mediaplayer.app.util.RemoteLogger.i("Player", "Catchup playback ended. No auto-switch.")
            return
        }

        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val globalCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)

        // ===== 手动指定内核模式：不做任何自动切换，仅 OSD 提示用户 =====
        if (globalCore != Prefs.PLAYER_CORE_AUTO) {
            val coreName = when (globalCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
                else -> "VLC"
            }
            tvOsdInfo?.text = "当前播放内核($coreName)无法播放此频道，请在设置中切换为智能模式"
            showOsd()
            com.mediaplayer.app.util.RemoteLogger.e("Player", "Manual core ($coreName) playback failed. No auto-switch in manual mode.")
            return
        }

        // 如果是网络超时，不要去切换内核（因为内核没毛病），直接走换线逻辑
        if (!isNetworkTimeout) {
            // 内核容灾：智能模式下尝试切换播放内核
            if (coreRetryLevel < 2) {
                coreRetryLevel++
                val coreName = when (coreRetryLevel) {
                    1 -> "VLC"
                    2 -> "IJKPlayer"
                    else -> "ExoPlayer"
                }
                Toast.makeText(this, "尝试使用 $coreName 重试...", Toast.LENGTH_SHORT).show()
                com.mediaplayer.app.util.RemoteLogger.i("Player", "Playback failed. Retrying with core: $coreName")
                playCurrentLineInTv()
                return
            }
        }

        val channel = allChannels.getOrNull(currentChannelIndex)
        val lines = channel?.getLinesSafely() ?: emptyList()

        if (lines.isNotEmpty() && currentLineIndex < lines.size - 1) {
            currentLineIndex++
            coreRetryLevel = 0
            Toast.makeText(this@MainActivity, "当前线路失效，切换线路 ${currentLineIndex + 1}...", Toast.LENGTH_SHORT).show()
            com.mediaplayer.app.util.RemoteLogger.i("Player", "Core failed. Switching to line ${currentLineIndex + 1}")
            playCurrentLineInTv()
        } else {
            coreRetryLevel = 0
            currentLineIndex = 0
            
            val msg = if (isNetworkTimeout) "网络超时，播放失败" else "所有线路均已失效"
            com.mediaplayer.app.util.RemoteLogger.e("Player", "All lines and cores failed for channel: ${channel?.name ?: "Unknown"}. Reason: $msg")
            Toast.makeText(this@MainActivity, msg, Toast.LENGTH_LONG).show()
            
            continuousSkipCount++
            if (continuousSkipCount >= maxAutoSkips) {
                Toast.makeText(this@MainActivity, "多个频道连续播放失败，已停止自动换台", Toast.LENGTH_LONG).show()
                continuousSkipCount = 0
            } else {
                Toast.makeText(this@MainActivity, "当前频道失效，自动为您跳过", Toast.LENGTH_SHORT).show()
                if (allChannels.isNotEmpty()) {
                    val nextChannelIndex = (currentChannelIndex + 1) % allChannels.size
                    uiHandler.postDelayed({
                        playTvChannel(nextChannelIndex, isAutoSkip = true)
                    }, 1000)
                }
            }
        }
    }

    private var resolveJob: kotlinx.coroutines.Job? = null
    private var playGeneration: Int = 0

    private fun playTvChannel(index: Int, isAutoSkip: Boolean = false) {
        if (!isAutoSkip) {
            continuousSkipCount = 0
        }
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return
        
        // 防止重复起播：如果已经在播放同一个频道，跳过
        if (index == currentChannelIndex && playerHelper?.isPlaying() == true) {
            return
        }
        
        // 取消上一个频道的 URL 解析协程，各播放器 play() 内部会自动 stop() 旧流。
        resolveJob?.cancel()
        resolveJob = null

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
        if (globalCore == 4) {
            globalCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, globalCore).apply()
        }
        
        var desiredCore = globalCore
        var coreText = ""
        
        if (globalCore == Prefs.PLAYER_CORE_AUTO) {
            if (coreRetryLevel > 0) {
                desiredCore = when (coreRetryLevel) {
                    1 -> { coreText = "容灾 (VLC)"; Prefs.PLAYER_CORE_VLC }
                    2 -> { coreText = "容灾 (IJK)"; Prefs.PLAYER_CORE_IJK }
                    else -> desiredCore
                }
            } else {
                desiredCore = when (line.streamType.lowercase()) {
                    "vlc" -> {
                        coreText = "智能 (VLC)"
                        Prefs.PLAYER_CORE_VLC
                    }
                    "ijk" -> {
                        coreText = "智能 (IJK)"
                        Prefs.PLAYER_CORE_IJK
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
            }
        } else {
            coreText = when (desiredCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
                else -> "VLC"
            }
        }
        
        findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvStreamType)?.text = "${line.streamType.uppercase()} ($coreText)"

        // 判断当前已经实例化的 playerHelper 是否与所需的一致
        val isCoreMatch = when (desiredCore) {
            Prefs.PLAYER_CORE_EXO -> playerHelper is com.mediaplayer.app.util.ExoPlayerHelper
            Prefs.PLAYER_CORE_IJK -> playerHelper is com.mediaplayer.app.util.IjkPlayerHelper
            else -> playerHelper is com.mediaplayer.app.util.VlcPlayerHelper
        }

        if (playerHelper == null || !isCoreMatch) {
            playerHelper?.release()
            videoLayout?.removeAllViews() // 清除旧的视图
            initPlayerWithCore(desiredCore)
        }
        progressBuffering?.visibility = View.VISIBLE

        resolveJob = lifecycleScope.launch {
            val gen = ++playGeneration
            val finalUrl = com.mediaplayer.app.util.StreamResolver.resolve(line.streamUrl, line.userAgent, line.customHeaders)
            
            // 如果在此期间又发生了切台，放弃本次播放，防止旧 resolve 协程覆盖新频道
            if (gen != playGeneration) {
                com.mediaplayer.app.util.RemoteLogger.i("Player", "Discarded stale play() for generation $gen (current: $playGeneration)")
                return@launch
            }
            
            val lowerUrl = finalUrl.lowercase()
            val streamTypeLower = line.streamType.lowercase()
            val isMulticastOrLive = lowerUrl.startsWith("udp://") || 
                                    lowerUrl.startsWith("rtp://") || 
                                    lowerUrl.contains(".ts") || 
                                    lowerUrl.contains(".flv") || 
                                    streamTypeLower in listOf("ts", "rtp", "udp", "flv")
            // 对于组播、ts、flv等特殊流，放行看门狗（不检测假死）
            isWatchdogEnabledForCurrentStream = !isMulticastOrLive
            
            currentPlaybackState = PlaybackState.BUFFERING
            stateStartTime = System.currentTimeMillis()
            playerHelper?.play(finalUrl, line.userAgent, line.customHeaders)
        }
        
        // 启动/重置看门狗
        currentPlaybackState = PlaybackState.BUFFERING
        stateStartTime = System.currentTimeMillis()
        lastPlaybackTime = 0L
        frozenTimeCounter = 0
        uiHandler.removeCallbacks(watchdogRunnable)
        uiHandler.postDelayed(watchdogRunnable, 2000)
        
        // 频道列表中高亮当前播放频道
        channelAdapter.setPlayingChannelId(channel.id)
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
                    val margin8 = resources.getDimensionPixelSize(R.dimen.dp_8)
                    setMargins(0, margin8, 0, margin8)
                }
                text = "线路 ${index + 1} (${line.streamType.uppercase()})"
                setTextSize(android.util.TypedValue.COMPLEX_UNIT_PX, resources.getDimension(R.dimen.sp_18))
                val pad32 = resources.getDimensionPixelSize(R.dimen.dp_32)
                val pad24 = resources.getDimensionPixelSize(R.dimen.dp_24)
                setPadding(pad32, pad24, pad32, pad24)
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
        activeListArea = "channels"
    }

    private fun showOsd() {
        // 刷新频道号码和名称（来自当前频道，不依赖播放器回调）
        val channel = allChannels.getOrNull(currentChannelIndex)
        if (channel != null) {
            tvOsdChannelNum?.text = String.format("%03d", channel.globalIndex + 1)
            tvOsdChannelName?.text = channel.name
        }
        
        // 如果正在播放但 tvOsdInfo 仍卡在"连接中"（播放器未上报分辨率/编码信息），
        // 至少显示解码模式和播放内核作为回退信息
        // 使用 playerHelper?.isPlaying() 而非 currentPlaybackState，以兼容 ExoPlayer
        // （ExoPlayerHelper 只在 onVideoSizeChanged 中调用 onPlaying，无视频尺寸时不回调）
        if (playerHelper?.isPlaying() == true) {
            val infoText = tvOsdInfo?.text?.toString() ?: ""
            if (infoText.contains("连接中") || infoText.isEmpty()) {
                val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
                val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
                val decoderStr = when (decoderMode) {
                    Prefs.DECODER_MODE_HARDWARE -> "硬解"
                    Prefs.DECODER_MODE_SOFTWARE -> "软解"
                    else -> "自动解码"
                }
                val coreStr = playerHelper?.let {
                    when (it) {
                        is com.mediaplayer.app.util.ExoPlayerHelper -> "ExoPlayer"
                        is com.mediaplayer.app.util.IjkPlayerHelper -> "IJKPlayer"
                        is com.mediaplayer.app.util.VlcPlayerHelper -> "VLC"
                        else -> ""
                    }
                } ?: ""
                tvOsdInfo?.text = if (coreStr.isNotEmpty()) "$decoderStr | $coreStr" else decoderStr
            }
        }

        layoutOsd?.visibility = View.VISIBLE
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "OSD VISIBLE")
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

    // ═══════════════════════════════════════════════════
    // SHARED LOGIC
    // ═══════════════════════════════════════════════════

    private fun setupAdapters() {
        groupAdapter = GroupAdapter(
            onClick = { group ->
                currentGroupId = group.id
                filterChannels(scrollToTop = true)
                groupAdapter.setSelected(group.id)
                tvChannelsRv?.requestFocus()
            },
            onFocus = { group ->
                android.util.Log.d("TV_FOCUS", "MainActivity received onFocus for: ${group.name}, isTvMode: $isTvMode")
                if (isTvMode) {
                    // 立即更新左侧分组的选中UI状态，不要有延迟
                    currentGroupId = group.id
                    groupAdapter.setSelected(group.id)
                    
                    focusDebounceRunnable?.let { focusDebounceHandler.removeCallbacks(it) }
                    val r = Runnable {
                        android.util.Log.d("TV_FOCUS", "Executing runnable for: ${group.name}")
                        filterChannels(scrollToTop = true)
                    }
                    focusDebounceRunnable = r
                    focusDebounceHandler.postDelayed(r, 150)
                }
            }
        )

        channelAdapter = ChannelAdapter(
            isTvMode = isTvMode,
            onClick = { channel, _ ->
                    val realIndex = allChannels.indexOf(channel)
                    playTvChannel(realIndex)
                    uiHandler.postDelayed(hideZappingRunnable, 500)
            }
        )
        
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        channelAdapter.showLogo = prefs.getBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, true)

        if (isTvMode) {
            tvGroupsRv?.apply {
                layoutManager = object : LinearLayoutManager(this@MainActivity) {
                    override fun onFocusSearchFailed(focused: View, focusDirection: Int, recycler: androidx.recyclerview.widget.RecyclerView.Recycler, state: androidx.recyclerview.widget.RecyclerView.State): View? {
                        val next = super.onFocusSearchFailed(focused, focusDirection, recycler, state)
                        if (next == null && (focusDirection == View.FOCUS_DOWN || focusDirection == View.FOCUS_UP)) {
                            return focused // 捕获焦点
                        }
                        return next
                    }
                    override fun requestChildRectangleOnScreen(parent: androidx.recyclerview.widget.RecyclerView, child: View, rect: android.graphics.Rect, immediate: Boolean, focusedChildVisible: Boolean): Boolean {
                        rect.top -= child.height * 2
                        rect.bottom += child.height * 2
                        return super.requestChildRectangleOnScreen(parent, child, rect, immediate, focusedChildVisible)
                    }
                }
                adapter = groupAdapter
            }
            tvChannelsRv?.apply {
                setHasFixedSize(true)
                layoutManager = object : LinearLayoutManager(this@MainActivity) {
                    override fun onFocusSearchFailed(focused: View, focusDirection: Int, recycler: androidx.recyclerview.widget.RecyclerView.Recycler, state: androidx.recyclerview.widget.RecyclerView.State): View? {
                        val next = super.onFocusSearchFailed(focused, focusDirection, recycler, state)
                        if (next == null && (focusDirection == View.FOCUS_DOWN || focusDirection == View.FOCUS_UP)) {
                            return focused // 捕获焦点，防止快速滚动时意外逃逸到侧边栏
                        }
                        return next
                    }
                    override fun requestChildRectangleOnScreen(parent: androidx.recyclerview.widget.RecyclerView, child: View, rect: android.graphics.Rect, immediate: Boolean, focusedChildVisible: Boolean): Boolean {
                        rect.top -= child.height * 2
                        rect.bottom += child.height * 2
                        return super.requestChildRectangleOnScreen(parent, child, rect, immediate, focusedChildVisible)
                    }
                }
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
        tvAuthWaiting?.visibility = View.GONE

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
        if (isLoadingData) return
        isLoadingData = true
        lifecycleScope.launch {
            try {
                // 1. 先拉分组列表
                val realGroups = repo.getGroups().getOrElse { emptyList() }
                groups = listOf(ChannelGroup(id = 0, name = "全部")) + realGroups
                groupAdapter.submitList(groups)
                groupAdapter.setSelected(0)

                // 2. 按分组并行拉取全量频道（彻底绕过全局 page_size 上限）
                repo.getAllChannelsByGroups(realGroups).onSuccess { list ->
                    list.forEachIndexed { index, channel ->
                        channel.globalIndex = index
                    }
                    allChannels = list
                    channelsByGroup = list.groupBy { it.groupId }
                    if (list.isNotEmpty()) {
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
                        currentGroupId = list[targetIndex].groupId
                        groupAdapter.setSelected(currentGroupId)
                        filterChannels(scrollToTop = false)
                        playTvChannel(targetIndex)
                        videoLayout?.requestFocus()
                    }
                }.onFailure {
                    // handle failure
                }
            } finally {
                isLoadingData = false
            }
        }
    }

    private fun showLoading(show: Boolean) {
        progressLoading?.visibility = if (show) View.VISIBLE else View.GONE
    }

    private fun showEmpty(show: Boolean, message: String = getString(R.string.no_channels)) {
        // layoutEmpty is not present in TV layout
    }

    private fun filterChannels(scrollToTop: Boolean = true) {
        filteredChannels = if (currentGroupId == 0L) {
            allChannels
        } else {
            channelsByGroup[currentGroupId] ?: emptyList()
        }
        channelAdapter.setData(filteredChannels)
        
        if (scrollToTop) {
            tvChannelsRv?.post {
                tvChannelsRv?.scrollToPosition(0) // 分组切换时，频道列表重置到顶部
            }
        }
    }


    private fun showEpgMenu() {
        if (currentChannelIndex < 0 || currentChannelIndex >= allChannels.size) return
        val channel = allChannels[currentChannelIndex]
        
        // 在显示 EPG 面板前主动更新焦点区域，防止 OnGlobalFocusChangeListener 拒绝合法焦点转移
        activeListArea = "epg"
        
        layoutZappingMenu?.visibility = View.GONE
        layoutSettingsMenu?.visibility = View.GONE
        layoutEpgMenu?.visibility = View.VISIBLE
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "EPG VISIBLE")
        
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
        activeListArea = "channels"
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

    private fun showZappingMenu(focusOnGroups: Boolean, resetToPlaying: Boolean = false) {
        if (layoutZappingMenu?.visibility == View.VISIBLE) return

        val playingChannel = if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) allChannels[currentChannelIndex] else null
        
        var groupChanged = false
        if (resetToPlaying) {
            val newGroupId = playingChannel?.groupId ?: 0L
            if (currentGroupId != newGroupId) {
                currentGroupId = newGroupId
                groupChanged = true
            }
        }
        
        if (groupChanged) {
            groupAdapter.setSelected(currentGroupId)
            filterChannels(scrollToTop = false)
        } else {
            groupAdapter.setSelected(currentGroupId)
        }
        
        // 先清除 videoLayout 上的焦点，防止其子 View 拦截后续按键事件
        videoLayout?.clearFocus()
        
        // 在设置可见性和请求焦点前主动更新焦点区域，防止 OnGlobalFocusChangeListener 拒绝合法焦点转移
        activeListArea = if (focusOnGroups) "groups" else "channels"
        
        // 必须在设置为 VISIBLE 之前封锁左侧焦点，否则 setVisibility 内部会瞬间触发原生焦点分配并篡改当前选中状态
        tvGroupsRv?.descendantFocusability = android.view.ViewGroup.FOCUS_BLOCK_DESCENDANTS

        layoutZappingMenu?.visibility = View.VISIBLE
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "ZappingMenu VISIBLE")
        uiHandler.removeCallbacks(hideZappingRunnable)
        uiHandler.postDelayed(hideZappingRunnable, 10000)

        if (focusOnGroups) {
            tvGroupsRv?.descendantFocusability = android.view.ViewGroup.FOCUS_AFTER_DESCENDANTS
            val groupIndex = groupAdapter.currentList.indexOfFirst { it.id == currentGroupId }
            if (groupIndex >= 0) {
                tvGroupsRv?.scrollToPosition(groupIndex)
                tvGroupsRv?.post {
                    val lm = tvGroupsRv?.layoutManager as? LinearLayoutManager
                    lm?.findViewByPosition(groupIndex)?.requestFocus() ?: tvGroupsRv?.requestFocus()
                }
            } else {
                tvGroupsRv?.requestFocus()
            }
        } else {
            val playingId = playingChannel?.id ?: -1L
            val indexInFiltered = filteredChannels.indexOfFirst { it.id == playingId }
            
            if (indexInFiltered >= 0) {
                tvChannelsRv?.scrollToPosition(indexInFiltered)
            }
            
            // 使用 postDelayed 确保布局完成后再请求焦点
            val requestFocusAction = {
                tvGroupsRv?.descendantFocusability = android.view.ViewGroup.FOCUS_AFTER_DESCENDANTS
                if (indexInFiltered >= 0) {
                    val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                    val view = lm?.findViewByPosition(indexInFiltered)
                    if (view != null) {
                        view.requestFocus()
                    } else {
                        // 如果视图还未布局完成，通过滚动触发再试一次
                        tvChannelsRv?.post {
                            val lm2 = tvChannelsRv?.layoutManager as? LinearLayoutManager
                            lm2?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                        }
                    }
                } else {
                    val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                    val firstVisible = lm?.findFirstVisibleItemPosition() ?: 0
                    lm?.findViewByPosition(firstVisible)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                }
            }
            
            tvChannelsRv?.postDelayed({ requestFocusAction() }, 100)
        }
    }

    private fun isViewDescendantOf(view: View, parent: View?): Boolean {
        if (parent == null) return false
        var p = view.parent
        while (p != null) {
            if (p === parent) return true
            p = p.parent
        }
        return false
    }

    private var isBouncingFocus = false

    private fun bounceFocusBack() {
        if (isBouncingFocus) return
        isBouncingFocus = true
        
        when (activeListArea) {
            "channels" -> {
                val rv = tvChannelsRv
                val lm = rv?.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
            "groups" -> {
                val rv = tvGroupsRv
                val lm = rv?.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
            "epg" -> {
                val rv = rvEpgList
                val lm = rv?.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
        }
        
        tvChannelsRv?.postDelayed({ isBouncingFocus = false }, 100)
    }

    override fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            val keyCode = event.keyCode
            
            com.mediaplayer.app.util.RemoteLogger.i("KeyEvent", "User pressed key $keyCode")

            // 只要面板处于显示状态，用户的任何按键都应当重置自动隐藏的时间
            if (layoutZappingMenu?.visibility == View.VISIBLE) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 15000)
            }
            if (layoutOsd?.visibility == View.VISIBLE) {
                uiHandler.removeCallbacks(hideOsdRunnable)
                uiHandler.postDelayed(hideOsdRunnable, 5000)
            }
            
            val focusedView = currentFocus
            if (focusedView != null) {
                // 跟踪用户的合法横向意图
                if (keyCode == KeyEvent.KEYCODE_DPAD_LEFT) {
                    if (isViewDescendantOf(focusedView, tvChannelsRv)) activeListArea = "groups"
                    else if (isViewDescendantOf(focusedView, rvEpgList)) activeListArea = "channels"
                } else if (keyCode == KeyEvent.KEYCODE_DPAD_RIGHT) {
                    if (isViewDescendantOf(focusedView, tvGroupsRv)) activeListArea = "channels"
                    else if (isViewDescendantOf(focusedView, tvChannelsRv)) activeListArea = "epg"
                }
            }

            // 【终极 TV 焦点防御系统】
            // 拦截 Activity 级别的所有按键分发。防止极速滚动时脱离列表边界。
            if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN || keyCode == KeyEvent.KEYCODE_DPAD_UP) {
                if (focusedView != null) {
                    val direction = if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN) View.FOCUS_DOWN else View.FOCUS_UP
                    val dirStr = if (direction == View.FOCUS_DOWN) "DOWN" else "UP"
                    
                    fun handleListFocus(rv: androidx.recyclerview.widget.RecyclerView, listName: String): Boolean {
                        val lm = rv.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                        val adapter = rv.adapter
                        val focusedPos = rv.getChildAdapterPosition(focusedView)
                        val nextFocus = android.view.FocusFinder.getInstance().findNextFocus(rv as android.view.ViewGroup, focusedView, direction)
                        
                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName $dirStr | currPos:$focusedPos, nextFocusPos:${nextFocus?.let { rv.getChildAdapterPosition(it) }}, lastVisible:${lm?.findLastVisibleItemPosition()}")

                        if (nextFocus != null) {
                            nextFocus.requestFocus()
                            return true
                        } else {
                            if (lm != null && adapter != null && focusedPos != androidx.recyclerview.widget.RecyclerView.NO_POSITION) {
                                if (direction == View.FOCUS_DOWN) {
                                    val nextPos = focusedPos + 1
                                    if (nextPos < adapter.itemCount) {
                                        rv.scrollToPosition(nextPos)
                                        rv.post { lm.findViewByPosition(nextPos)?.requestFocus() }
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName Blocked escape DOWN. Snap to $nextPos")
                                    } else {
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName reached BOTTOM.")
                                    }
                                } else {
                                    val nextPos = focusedPos - 1
                                    if (nextPos >= 0) {
                                        rv.scrollToPosition(nextPos)
                                        rv.post { lm.findViewByPosition(nextPos)?.requestFocus() }
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName Blocked escape UP. Snap to $nextPos")
                                    } else {
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName reached TOP.")
                                    }
                                }
                            }
                            return true // 始终吞噬，绝对不让 Android 全局接管焦点！
                        }
                    }

                    if (isViewDescendantOf(focusedView, tvChannelsRv)) {
                        return handleListFocus(tvChannelsRv as androidx.recyclerview.widget.RecyclerView, "ChannelList")
                    } else if (isViewDescendantOf(focusedView, tvGroupsRv)) {
                        return handleListFocus(tvGroupsRv as androidx.recyclerview.widget.RecyclerView, "GroupList")
                    } else if (isViewDescendantOf(focusedView, rvEpgList)) {
                        return handleListFocus(rvEpgList as androidx.recyclerview.widget.RecyclerView, "EpgList")
                    } else {
                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "OtherArea $dirStr | focusedId:${focusedView.id}")
                    }
                }
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
        } else if (event.action == KeyEvent.ACTION_UP) {
            val keyCode = event.keyCode
            if (isTvMode && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
                val isSettingsVisible = layoutSettingsMenu?.visibility == View.VISIBLE
                val isEpgVisible = layoutEpgMenu?.visibility == View.VISIBLE
                val isLineVisible = layoutLineMenu?.visibility == View.VISIBLE
                val anyPanelOpen = isMenuVisible || isSettingsVisible || isEpgVisible || isLineVisible

                if (!anyPanelOpen) {
                    // 【焦点修复】拦截焦点遗留在频道列表项上的 OK 事件，防止触发换台
                    // 改为显示 OSD（用户可通过 LEFT 键呼出频道列表）
                    val focusedView = currentFocus
                    if (focusedView != null && isViewDescendantOf(focusedView, tvChannelsRv)) {
                        com.mediaplayer.app.util.RemoteLogger.i("KeyEvent", "OK on channel item - intercepted for OSD")
                        showOsd()
                        return true
                    }
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

            val reverseChannels = getSharedPreferences(Prefs.FILE, MODE_PRIVATE).getBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, false)
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> {
                    if (!anyPanelOpen) {
                        val targetIdx = if (reverseChannels) {
                            if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        } else {
                            if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        }
                        playTvChannel(targetIdx)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (!anyPanelOpen) {
                        val targetIdx = if (reverseChannels) {
                            if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        } else {
                            if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        }
                        playTvChannel(targetIdx)
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
                        showZappingMenu(focusOnGroups = false, resetToPlaying = true)
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
            val isEpgVisible = layoutEpgMenu?.visibility == View.VISIBLE
            val isLineVisible = layoutLineMenu?.visibility == View.VISIBLE

            if (!isMenuVisible && !isSettingsVisible && !isEpgVisible && !isLineVisible && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                if (event?.isTracking == true && !event.isCanceled) {
                    // 短按 OK 键，显示 OSD（5s 自动隐藏）
                    showOsd()
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
        
        // 首次 onResume 由 onCreate 的认证链路负责数据加载，跳过以避免并发
        if (isFirstResume) {
            isFirstResume = false
            return
        }
        
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

    override fun onConfigurationChanged(newConfig: android.content.res.Configuration) {
        super.onConfigurationChanged(newConfig)
        
        // Pad 或设备旋转时，系统触发横竖屏切换或屏幕尺寸变化
        // 由于我们在 manifest 中声明了 configChanges="orientation|screenSize"，Activity 不会重建
        // 在这里可以安全地调整 UI 或通知播放器重新计算尺寸，防止 Surface 尺寸异常导致闪退
        videoLayout?.requestLayout()
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
