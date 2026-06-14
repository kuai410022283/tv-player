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

    internal val appLifecycleDelegate = com.mediaplayer.app.ui.home.delegates.AppLifecycleDelegate(this)
    internal val playbackLifecycleDelegate = com.mediaplayer.app.ui.home.delegates.PlaybackLifecycleDelegate(this)

    override fun getResources(): android.content.res.Resources {
        val res = super.getResources()
        val dm = res.displayMetrics
        val config = res.configuration
        
        if (dm.widthPixels > 0 && dm.heightPixels > 0) {
            val shortSide = Math.min(dm.widthPixels, dm.heightPixels)
            
            // 嗅探设备类型 (是否为 Android TV)
            val isTv = (config.uiMode and android.content.res.Configuration.UI_MODE_TYPE_MASK) == android.content.res.Configuration.UI_MODE_TYPE_TELEVISION
            
            if (isTv) {
                // TV端：强制映射为 720dp 宽度，配合我们的标准 dp/sp 产生适合远距离观看的大字号
                val targetDensity = shortSide / 720f
                if (Math.abs(dm.density - targetDensity) > 0.01f) {
                    val targetScaledDensity = targetDensity * (dm.scaledDensity / dm.density)
                    val targetDensityDpi = (160 * targetDensity).toInt()
                    dm.density = targetDensity
                    dm.scaledDensity = targetScaledDensity
                    dm.densityDpi = targetDensityDpi
                }
            } else {
                // Phone / Pad: 放权给 Android 系统原生 DisplayMetrics 引擎进行缩放适配，不作干预
            }
        }
        return res
    }

    internal val repo = ChannelRepository()
    internal lateinit var authManager: ClientAuthManager
    internal var isTvMode = true

    // ── Views (TV mode - Zapping & Player) ──
    internal var tvGroupsRv: RecyclerView? = null
    internal var tvChannelsRv: RecyclerView? = null
    private var tvAuthWaiting: View? = null
    private var layoutZappingMenu: View? = null
    private var layoutOsd: View? = null
    private var tvOsdChannelNum: TextView? = null
    private var tvOsdChannelName: TextView? = null
    private var tvOsdInfo: TextView? = null
    private var tvOsdEpg: TextView? = null
    private var progressEpg: ProgressBar? = null
    private var progressBuffering: ProgressBar? = null
    internal var videoLayout: android.widget.FrameLayout? = null
    
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
    internal var configWebServer: com.mediaplayer.app.server.ConfigWebServer? = null
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
    internal var allChannels = listOf<Channel>()
    internal var channelsByGroup: Map<Long, List<Channel>> = emptyMap()
    internal var filteredChannels = listOf<Channel>()
    internal var currentGroupId = 0L
    internal lateinit var playbackSessionManager: com.mediaplayer.app.core.player.PlaybackSessionManager
    internal var currentChannelIndex = 0

    internal lateinit var groupAdapter: GroupAdapter
    internal lateinit var channelAdapter: ChannelAdapter
    private lateinit var zappingMenuDelegate: com.mediaplayer.app.ui.home.delegates.ZappingMenuDelegate
    private lateinit var epgMenuDelegate: com.mediaplayer.app.ui.home.delegates.EpgMenuDelegate
    internal lateinit var playerOverlayDelegate: com.mediaplayer.app.ui.home.delegates.PlayerOverlayDelegate
    private lateinit var touchGestureDelegate: com.mediaplayer.app.ui.home.delegates.TouchGestureDelegate
    private lateinit var settingsMenuDelegate: com.mediaplayer.app.ui.home.delegates.SettingsMenuDelegate
    private lateinit var keyDispatcherDelegate: com.mediaplayer.app.ui.home.delegates.KeyDispatcherDelegate

    internal val authPollHandler = Handler(Looper.getMainLooper())
    internal var authPollRunnable: Runnable? = null
    internal val heartbeatHandler = Handler(Looper.getMainLooper())
    internal var heartbeatRunnable: Runnable? = null
    internal val focusDebounceHandler = Handler(Looper.getMainLooper())
    internal var focusDebounceRunnable: Runnable? = null

    internal val uiHandler = Handler(Looper.getMainLooper())
    
    // 数据加载并发锁
    internal var isLoadingData = false
    // 首次 onResume 标记（防止与 onCreate 的认证链路冲突）
    private var isFirstResume = true
    internal var hasShownSplash = false


    internal val hideZappingRunnable = Runnable { 
        layoutZappingMenu?.visibility = View.GONE
        activeListArea = "channels"
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "ZappingMenu GONE")
    }

    // System Announcement state
    internal var sysAnnouncement: String? = null
    internal var sysAnnouncementInterval: Int = 0
    internal var marqueeIsVisible = false
    internal val marqueeRunnable = Runnable { appLifecycleDelegate.triggerMarquee() }
    internal val hideMarqueeRunnable = Runnable { 
        findViewById<android.view.View>(R.id.layoutAnnouncement)?.visibility = View.GONE
        findViewById<android.widget.TextView>(R.id.tvAnnouncement)?.isSelected = false
        marqueeIsVisible = false
        if (sysAnnouncementInterval > 0) {
            uiHandler.postDelayed(marqueeRunnable, sysAnnouncementInterval * 60 * 1000L)
        }
    }

    internal var activeListArea = "channels" // "groups", "channels", "epg"

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

        appLifecycleDelegate.setupAdapters()
        
        zappingMenuDelegate = com.mediaplayer.app.ui.home.delegates.ZappingMenuDelegate(
            layoutZappingMenu = layoutZappingMenu!!,
            tvGroupsRv = tvGroupsRv!!,
            tvChannelsRv = tvChannelsRv!!,
            groupAdapter = groupAdapter,
            channelAdapter = channelAdapter,
            getCurrentGroupId = { currentGroupId }
        )

        appLifecycleDelegate.checkAuthAndLoad()
        
        // 检查版本更新
        com.mediaplayer.app.util.UpdateManager.checkUpdate(this, lifecycleScope, false)
    }



    private fun hideSystemUI() {
        WindowInsetsControllerCompat(window, window.decorView).let { controller ->
            controller.hide(WindowInsetsCompat.Type.systemBars())
            controller.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }
    }

    private var initialBrightness = -1f
    private var initialVolume = -1
    private var isAdjusting = false
    private var adjustMode = 0 // 0: none, 1: brightness, 2: volume

    // ── Touch Gestures for Mobile/Tablet ──
    private fun setupTouchGestures() {
        touchGestureDelegate = com.mediaplayer.app.ui.home.delegates.TouchGestureDelegate(
            activity = this,
            videoLayout = videoLayout!!,
            callbacks = object : com.mediaplayer.app.ui.home.delegates.TouchGestureDelegate.Callbacks {
                override fun onSingleTap() {
                    var menuHidden = false
                    if (zappingMenuDelegate.isVisible()) {
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        hideZappingRunnable.run()
                        menuHidden = true
                    }
                    if (epgMenuDelegate.isVisible()) {
                        hideEpgMenu()
                        menuHidden = true
                    }
                    if (playerOverlayDelegate.isSettingsVisible()) {
                        hideSettingsMenu()
                        menuHidden = true
                    }
                    if (menuHidden) return
                    showOsd()
                }

                override fun onLongPress() {
                    showLineSelectionMenu()
                }

                override fun onDoubleTap() {
                    if (playerOverlayDelegate.isSettingsVisible()) hideSettingsMenu() else showSettingsMenu()
                }

                override fun onSwipeUp() {
                    if (!zappingMenuDelegate.isVisible() && !epgMenuDelegate.isVisible()) {
                        val next = if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        playbackLifecycleDelegate.playTvChannel(next)
                    }
                }

                override fun onSwipeDown() {
                    if (!zappingMenuDelegate.isVisible() && !epgMenuDelegate.isVisible()) {
                        val prev = if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        playbackLifecycleDelegate.playTvChannel(prev)
                    }
                }

                override fun onSwipeLeft() {
                    if (zappingMenuDelegate.isVisible()) {
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        hideZappingRunnable.run()
                    } else if (!epgMenuDelegate.isVisible() && !playerOverlayDelegate.isSettingsVisible()) {
                        showEpgMenu()
                    }
                }

                override fun onSwipeRight() {
                    if (epgMenuDelegate.isVisible()) {
                        hideEpgMenu()
                    } else if (!zappingMenuDelegate.isVisible() && !playerOverlayDelegate.isSettingsVisible()) {
                        showZappingMenu(focusOnGroups = false, resetToPlaying = true)
                    }
                }
            }
        )
        touchGestureDelegate.attach()
    }


    // ═══════════════════════════════════════════════════
    // TV MODE SETUP & PLAYER
    // ═══════════════════════════════════════════════════



    private fun setupTvViews() {
        val vLayout = findViewById<android.view.ViewGroup>(com.mediaplayer.app.R.id.videoLayout)

        playbackSessionManager = com.mediaplayer.app.core.player.PlaybackSessionManager(
            context = this,
            videoLayout = vLayout!!,
            prefs = getSharedPreferences(com.mediaplayer.app.Prefs.FILE, MODE_PRIVATE),
            coroutineScope = lifecycleScope
        )

        lifecycleScope.launch {
            playbackSessionManager.events.collect { event ->
                when (event) {
                    is com.mediaplayer.app.core.player.PlaybackEvent.OsdUpdate -> {
                        val fullInfo = buildString {
                            append(if (event.linesCount > 1) "连接中... (线路 ${event.lineIndex + 1}/${event.linesCount})" else "连接中...")
                            append(" | ")
                            append(event.streamType.uppercase())
                            append(" | ")
                            append(event.coreName)
                        }
                        findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvOsdInfo)?.text = fullInfo
                        
                        val tvOsdChannelNum = findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvOsdChannelNum)
                        val tvOsdChannelName = findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvOsdChannelName)
                        tvOsdChannelNum?.text = String.format("%03d", event.channel.globalIndex + 1)
                        tvOsdChannelName?.text = event.channel.name
                        
                        playbackLifecycleDelegate.loadEpgForChannel(event.channel)
                        showOsd()
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.Error -> {
                        android.widget.Toast.makeText(this@MainActivity, event.message, android.widget.Toast.LENGTH_SHORT).show()
                        playerOverlayDelegate.setOsdInfo(event.message)
                        playerOverlayDelegate.showOsd(null, playbackSessionManager.playerHelper)
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.RequestSkipChannel -> {
                        if (!event.isContinuousSkip) {
                            val nextIdx = (currentChannelIndex + 1) % allChannels.size
                            playbackLifecycleDelegate.playTvChannel(nextIdx, isAutoSkip = true)
                        }
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.StateChanged -> {
                        // 状态变更：此处不操作 progressBuffering（由 BufferingUiUpdate 单点控制，避免双重竞态闪屏）
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.BufferingUiUpdate -> {
                        findViewById<android.view.View>(com.mediaplayer.app.R.id.progressBuffering)?.visibility = if (event.visible) android.view.View.VISIBLE else android.view.View.GONE
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.WatchdogAlert -> {
                        android.widget.Toast.makeText(this@MainActivity, event.message, android.widget.Toast.LENGTH_SHORT).show()
                    }
                    is com.mediaplayer.app.core.player.PlaybackEvent.FormatInfo -> {
                        val tvInfo = findViewById<android.widget.TextView>(com.mediaplayer.app.R.id.tvOsdInfo)
                        val prefs = getSharedPreferences(com.mediaplayer.app.Prefs.FILE, MODE_PRIVATE)
                        val decoderMode = prefs.getInt(com.mediaplayer.app.Prefs.KEY_DECODER_MODE, com.mediaplayer.app.Prefs.DECODER_MODE_AUTO)
                        val decoderStr = when (decoderMode) {
                            com.mediaplayer.app.Prefs.DECODER_MODE_HARDWARE -> "硬解"
                            com.mediaplayer.app.Prefs.DECODER_MODE_SOFTWARE -> "软解"
                            else -> "自动解码"
                        }
                        val coreStr = playbackSessionManager.playerHelper?.let {
                            when (it) {
                                is com.mediaplayer.app.util.ExoPlayerHelper -> "ExoPlayer"
                                is com.mediaplayer.app.util.IjkPlayerHelper -> "IJKPlayer"
                                is com.mediaplayer.app.util.VlcPlayerHelper -> "VLC"
                                else -> ""
                            }
                        } ?: ""
                        
                        val baseInfo = if (coreStr.isNotEmpty()) "$decoderStr | $coreStr" else decoderStr
                        val fullInfo = buildString {
                            if (event.resolution.isNotEmpty() && event.resolution != "VLC") append(event.resolution)
                            if (baseInfo.isNotEmpty()) {
                                if (isNotEmpty()) append(" | ")
                                append(baseInfo)
                            }
                        }
                        
                        tvInfo?.text = fullInfo
                        playerOverlayDelegate.extendOsdTimeout()
                    }
                }
            }
        }

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
        epgMenuDelegate = com.mediaplayer.app.ui.home.delegates.EpgMenuDelegate(
            layoutEpgMenu = findViewById(com.mediaplayer.app.R.id.layoutEpgMenu),
            rvEpgList = findViewById(com.mediaplayer.app.R.id.rvEpgList),
            tvEpgMenuTitle = findViewById(com.mediaplayer.app.R.id.tvEpgMenuTitle),
            tvEpgEmptyText = findViewById(com.mediaplayer.app.R.id.tvEpgEmptyText),
            progressEpgLoading = findViewById(com.mediaplayer.app.R.id.progressEpgLoading),
            epgAdapter = epgAdapter,
            coroutineScope = lifecycleScope,
            onPlayCatchup = { prog ->
                val channel = allChannels.getOrNull(currentChannelIndex) ?: return@EpgMenuDelegate
                try {
                    val sdf = java.text.SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", java.util.Locale.getDefault())
                    val startUnix = sdf.parse(prog.startTime)?.time?.div(1000) ?: 0L
                    val endUnix = sdf.parse(prog.endTime)?.time?.div(1000) ?: 0L
                    if (startUnix > 0 && endUnix > 0) {
                        val url = com.mediaplayer.app.data.api.ApiClient.getCatchupUrl(channel.id, startUnix, endUnix)
                        val lines = channel.getLinesSafely()
                        val ua: String = if (lines.isNotEmpty()) lines[0].userAgent ?: "" else ""
                        val headers: String = if (lines.isNotEmpty()) lines[0].customHeaders ?: "" else ""
                        
                        if (isTvMode) {
                            currentCatchupStartTime = prog.startTime
                            currentCatchupChannelIndex = currentChannelIndex
                            tvOsdInfo?.text = "回看: ${prog.title}"
                            playbackSessionManager.playCatchup(url, ua, headers)
                            hideEpgMenu()
                        } else {
                            val intent = android.content.Intent(this@MainActivity, com.mediaplayer.app.ui.player.PlayerActivity::class.java).apply {
                                putExtra("channel_id", channel.id)
                                putExtra("channel_name", channel.name)
                                putExtra("stream_url", url)
                                putExtra("stream_type", "hls")
                                putExtra("user_agent", ua)
                                putExtra("custom_headers", headers)
                            }
                            startActivity(intent)
                        }
                    } else {
                        android.widget.Toast.makeText(this@MainActivity, "节目时间解析失败", android.widget.Toast.LENGTH_SHORT).show()
                    }
                } catch (e: Exception) {
                    android.widget.Toast.makeText(this@MainActivity, "回看失败: ${e.message}", android.widget.Toast.LENGTH_SHORT).show()
                }
            },
            fetchEpgData = { epgId ->
                val response = com.mediaplayer.app.data.api.ApiClient.getService().getEPG(epgId)
                if (response.isSuccessful && response.body()?.code == 0) {
                    response.body()?.data
                } else {
                    null
                }
            }
        )
        
        // Line Menu
        layoutLineMenu = findViewById(R.id.layoutLineMenu)
        tvLineMenuTitle = findViewById(R.id.tvLineMenuTitle)
        containerLines = findViewById(R.id.containerLines)

        playerOverlayDelegate = com.mediaplayer.app.ui.home.delegates.PlayerOverlayDelegate(
            layoutOsd = findViewById(com.mediaplayer.app.R.id.layoutOsd),
            tvOsdChannelNum = findViewById(com.mediaplayer.app.R.id.tvOsdChannelNum),
            tvOsdChannelName = findViewById(com.mediaplayer.app.R.id.tvOsdChannelName),
            tvOsdInfo = findViewById(com.mediaplayer.app.R.id.tvOsdInfo),
            tvOsdEpg = findViewById(com.mediaplayer.app.R.id.tvOsdEpg),
            layoutSettingsMenu = findViewById(com.mediaplayer.app.R.id.layoutSettingsMenu),
            layoutLineMenu = findViewById(com.mediaplayer.app.R.id.layoutLineMenu)
        )
        
        settingsMenuDelegate = com.mediaplayer.app.ui.home.delegates.SettingsMenuDelegate(this)
        keyDispatcherDelegate = com.mediaplayer.app.ui.home.delegates.KeyDispatcherDelegate(this)
        settingsMenuDelegate.setupSettingsViews()

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

    internal var currentCore = Prefs.PLAYER_CORE_AUTO


    internal fun showSettingsMenu() {
        if (layoutSettingsMenu?.visibility == View.VISIBLE) return
        
        // 隐藏左侧菜单
        layoutZappingMenu?.visibility = View.GONE
        
        playerOverlayDelegate.showSettings()
        activeListArea = "settings"
        android.util.Log.d("Navigation", "Active list area set to: $activeListArea")
        
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
            appLifecycleDelegate.setupQrConfigServer {
                hideSettingsMenu()
                Toast.makeText(this@MainActivity, "配置已保存，重新加载中...", Toast.LENGTH_LONG).show()
                appLifecycleDelegate.checkAuthAndLoad()
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



    internal fun hideSettingsMenu() {
        playerOverlayDelegate.hideSettings()
        activeListArea = "channels"
        android.util.Log.d("Navigation", "Active list area set to: $activeListArea")
    }



    private fun showLineSelectionMenu() {
        val channel = allChannels.getOrNull(currentChannelIndex) ?: return
        val lines = channel.getLinesSafely()
        if (lines.size <= 1) {
            Toast.makeText(this, "当前频道只有一条线路", Toast.LENGTH_SHORT).show()
            return
        }
        
        layoutZappingMenu?.visibility = View.GONE
        playerOverlayDelegate.hideSettings()
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
                
                if (index == playbackSessionManager.currentLineIndex) {
                    setTextColor(android.graphics.Color.parseColor("#FFC107"))
                    text = "线路 ${index + 1} (${line.streamType.uppercase()}) - 当前"
                } else {
                    setTextColor(android.graphics.Color.WHITE)
                }
                
                setOnClickListener {
                    if (playbackSessionManager.currentLineIndex != index) {
                        Toast.makeText(this@MainActivity, "已手动切换至线路 ${index + 1}", Toast.LENGTH_SHORT).show()
                        playbackSessionManager.switchLine(index)
                    }
                    hideLineSelectionMenu()
                }
            }
            containerLines?.addView(tv)
            if (index == playbackSessionManager.currentLineIndex) {
                firstFocusableView = tv
            }
        }
        
        playerOverlayDelegate.showLineSelection()
        layoutLineMenu?.post {
            val target = firstFocusableView ?: containerLines?.getChildAt(0)
            target?.requestFocus()
        }
    }

    internal fun hideLineSelectionMenu() {
        playerOverlayDelegate.hideLineSelection()
        activeListArea = "channels"
    }
    internal fun showOsd() {
        val channel = allChannels.getOrNull(currentChannelIndex)
        playerOverlayDelegate.showOsd(channel, playbackSessionManager.playerHelper)
        
        // 换台时同步触发跑马灯
        if (!sysAnnouncement.isNullOrEmpty() && !marqueeIsVisible) {
            appLifecycleDelegate.triggerMarquee()
        }
    }


    // ═══════════════════════════════════════════════════

    // ═══════════════════════════════════════════════════
    // SHARED LOGIC
    // ═══════════════════════════════════════════════════



    override fun onActivityResult(requestCode: Int, resultCode: Int, data: android.content.Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 1001) {
            // 开机广告结束或跳过，继续加载内容
            appLifecycleDelegate.showContent()
        }
    }


    private fun showLoading(show: Boolean) {
        progressLoading?.visibility = if (show) View.VISIBLE else View.GONE
    }

    private fun showEmpty(show: Boolean, message: String = getString(R.string.no_channels)) {
        // layoutEmpty is not present in TV layout
    }

    internal fun showEpgMenu() {
        if (currentChannelIndex < 0 || currentChannelIndex >= allChannels.size) return
        val channel = allChannels[currentChannelIndex]
        
        activeListArea = "epg"
        layoutZappingMenu?.visibility = android.view.View.GONE
        playerOverlayDelegate.hideSettings()
        com.mediaplayer.app.util.RemoteLogger.i("PanelTrace", "EPG VISIBLE")
        
        epgMenuDelegate.show(channel, currentCatchupChannelIndex == currentChannelIndex, currentCatchupStartTime)
    }


    internal fun hideEpgMenu() {
        layoutEpgMenu?.visibility = View.GONE
        activeListArea = "channels"
    }

    // ── Number Input State ──
    

    override fun dispatchTouchEvent(ev: android.view.MotionEvent): Boolean {
        if (ev.action == android.view.MotionEvent.ACTION_DOWN || ev.action == android.view.MotionEvent.ACTION_MOVE) {
            if (layoutZappingMenu?.visibility == View.VISIBLE) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 15000)
            }
        }
        return super.dispatchTouchEvent(ev)
    }
    internal fun showZappingMenu(focusOnGroups: Boolean, resetToPlaying: Boolean = false) {
        if (zappingMenuDelegate.isVisible()) return
        
        // 先清除 videoLayout 上的焦点，防止其子 View 拦截后续按键事件 (遗漏补回)
        videoLayout?.clearFocus()
        
        val playingChannel = if (currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) allChannels[currentChannelIndex] else null
        var groupChanged = false
        
        if (resetToPlaying && playingChannel != null) {
            if (currentGroupId != playingChannel.groupId) {
                currentGroupId = playingChannel.groupId
                groupChanged = true
            }
        }
        
        if (groupChanged) {
            groupAdapter.setSelected(currentGroupId)
            appLifecycleDelegate.filterChannels(scrollToTop = false)
        } else {
            groupAdapter.setSelected(currentGroupId)
        }
        
        activeListArea = if (focusOnGroups) "groups" else "channels"
        zappingMenuDelegate.show(activeListArea, currentGroupId)
        
        uiHandler.removeCallbacks(hideZappingRunnable)
        uiHandler.postDelayed(hideZappingRunnable, 10000)
    }


    internal fun isViewDescendantOf(view: View, parent: View?): Boolean {
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
        if (keyDispatcherDelegate.dispatchKeyEvent(event)) return true
        return super.dispatchKeyEvent(event)
    }


    // ── TV key events ──────────────────────────────────
    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        // 当任何面板未显示时，开始追踪 OK 键的长按事件 (遗漏补回)
        val isAnyPanelOpen = zappingMenuDelegate.isVisible() || playerOverlayDelegate.isSettingsVisible() || (layoutEpgMenu?.visibility == android.view.View.VISIBLE) || playerOverlayDelegate.isLineSelectionVisible()
        if (!isAnyPanelOpen && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
            event?.startTracking()
        }
        
        if (keyDispatcherDelegate.onKeyDown(keyCode, event)) return true
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
            appLifecycleDelegate.loadData()
            settingsChanged = false
        } else if (isTvMode && allChannels.isNotEmpty() && currentChannelIndex >= 0 && currentChannelIndex < allChannels.size) {
            // 直播流在切后台后会断开或缓冲失效，必须重新连接加载
            videoLayout?.post {
                playbackLifecycleDelegate.playTvChannel(currentChannelIndex)
            }
        }
    }
    
    override fun onPause() {
        super.onPause()
        if (isTvMode) {
            // 直播流切后台直接彻底停止，释放硬件解码器和网络连接
            playbackSessionManager.release()
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
        authPollHandler.removeCallbacksAndMessages(null)
        heartbeatHandler.removeCallbacksAndMessages(null)
        focusDebounceHandler.removeCallbacksAndMessages(null)
        uiHandler.removeCallbacksAndMessages(null)
        playerOverlayDelegate.onDestroy()
        
        playbackSessionManager.release()
    }
}
