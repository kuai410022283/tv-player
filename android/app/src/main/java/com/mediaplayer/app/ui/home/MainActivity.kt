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
    private var videoLayout: VLCVideoLayout? = null
    
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
    private var btnSettingsCancel: View? = null
    private var btnSettingsSave: View? = null
    private var tvSettingsInfo: TextView? = null

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

    private lateinit var groupAdapter: GroupAdapter
    private lateinit var channelAdapter: ChannelAdapter

    private val authPollHandler = Handler(Looper.getMainLooper())
    private var authPollRunnable: Runnable? = null
    private val heartbeatHandler = Handler(Looper.getMainLooper())
    private var heartbeatRunnable: Runnable? = null

    // ── VLC Player (TV Only) ──
    private var playerHelper: VlcPlayerHelper? = null
    private var retryCount = 0
    private val maxRetries = 3
    private val uiHandler = Handler(Looper.getMainLooper())
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
        super.onCreate(savedInstanceState)

        // 强制所有设备使用 TV 的沉浸式界面大一统！
        isTvMode = true
        
        // 保持屏幕常亮，防止手机/Pad自动锁屏
        window.addFlags(android.view.WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        setContentView(R.layout.activity_main)
        setupTvViews()
        initVlcPlayer()
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
                        tvChannelsRv?.post {
                            val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                            lm?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                        }
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
        btnSettingsCancel = findViewById(R.id.btnSettingsCancel)
        btnSettingsSave = findViewById(R.id.btnSettingsSave)
        tvSettingsInfo = findViewById(R.id.tvSettingsInfo)

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
    
    // 临时存储设置状态，点保存时才写入
    private var tempDecoderMode = Prefs.DECODER_MODE_AUTO
    private var tempScaleMode = Prefs.SCALE_MODE_DEFAULT
    private var tempAutoStart = true

    private fun setupSettingsViews() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val url = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL)
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        
        tempDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        tempScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        tempAutoStart = prefs.getBoolean(Prefs.KEY_AUTO_START, true)

        btnSettingsDecoder = findViewById(R.id.btnSettingsDecoder)
        btnSettingsScale = findViewById(R.id.btnSettingsScale)
        val btnSettingsAutoStart = findViewById<View>(R.id.btnSettingsAutoStart)
        
        fun updateDecoderText() {
            findViewById<TextView>(R.id.tvSettingsDecoderValue)?.text = when (tempDecoderMode) {
                Prefs.DECODER_MODE_HARDWARE -> "强制硬解"
                Prefs.DECODER_MODE_SOFTWARE -> "强制软解"
                else -> "自动识别"
            }
        }
        
        fun updateScaleText() {
            findViewById<TextView>(R.id.tvSettingsScaleValue)?.text = when (tempScaleMode) {
                Prefs.SCALE_MODE_STRETCH -> "强制 16:9"
                Prefs.SCALE_MODE_CROP -> "放大裁剪"
                Prefs.SCALE_MODE_4_3 -> "强制 4:3"
                else -> "原始比例"
            }
        }

        fun updateAutoStartText() {
            findViewById<TextView>(R.id.tvSettingsAutoStartValue)?.text = if (tempAutoStart) "开" else "关"
        }
        
        updateDecoderText()
        updateScaleText()
        updateAutoStartText()
        
        btnSettingsDecoder?.setOnClickListener {
            tempDecoderMode = when (tempDecoderMode) {
                Prefs.DECODER_MODE_AUTO -> Prefs.DECODER_MODE_HARDWARE
                Prefs.DECODER_MODE_HARDWARE -> Prefs.DECODER_MODE_SOFTWARE
                else -> Prefs.DECODER_MODE_AUTO
            }
            updateDecoderText()
        }
        
        btnSettingsScale?.setOnClickListener {
            tempScaleMode = when (tempScaleMode) {
                Prefs.SCALE_MODE_DEFAULT -> Prefs.SCALE_MODE_STRETCH
                Prefs.SCALE_MODE_STRETCH -> Prefs.SCALE_MODE_CROP
                Prefs.SCALE_MODE_CROP -> Prefs.SCALE_MODE_4_3
                else -> Prefs.SCALE_MODE_DEFAULT
            }
            updateScaleText()
        }

        btnSettingsAutoStart?.setOnClickListener {
            tempAutoStart = !tempAutoStart
            updateAutoStartText()
        }

        val btnSettingsCheckUpdate = findViewById<View>(R.id.btnSettingsCheckUpdate)
        btnSettingsCheckUpdate?.setOnClickListener {
            com.mediaplayer.app.util.UpdateManager.checkUpdate(this, lifecycleScope, true)
        }

        etSettingsUrl?.setText(url)
        
        // cacheMs: 500 to 5000, step 100.
        // progress: 0 to 45
        val progress = ((cacheMs - 500) / 100).coerceIn(0, 45)
        sbSettingsCache?.progress = progress
        tvSettingsCacheValue?.text = " ${cacheMs / 1000f} 秒"

        sbSettingsCache?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                val newCacheMs = 500 + progress * 100
                tvSettingsCacheValue?.text = " ${newCacheMs / 1000f} 秒"
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {}
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

        btnSettingsCancel?.setOnClickListener {
            hideSettingsMenu()
        }

        btnSettingsSave?.setOnClickListener {
            val newUrl = etSettingsUrl?.text?.toString()?.trim() ?: ""
            val newCacheMs = 500 + (sbSettingsCache?.progress ?: 0) * 100
            
            if (newUrl.isEmpty()) {
                Toast.makeText(this, "请输入服务器地址", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }

            val oldUrl = prefs.getString(Prefs.KEY_SERVER_URL, "")
            val oldDecoder = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
            if (newUrl != oldUrl || tempDecoderMode != oldDecoder) {
                authManager.clearAuth()
                com.mediaplayer.app.data.api.ApiClient.reset()
                settingsChanged = true
            }

            prefs.edit()
                .putString(Prefs.KEY_SERVER_URL, newUrl)
                .putInt(Prefs.KEY_NETWORK_CACHE, newCacheMs)
                .putInt(Prefs.KEY_DECODER_MODE, tempDecoderMode)
                .putInt(Prefs.KEY_SCALE_MODE, tempScaleMode)
                .putBoolean(Prefs.KEY_AUTO_START, tempAutoStart)
                .apply()
                
            com.mediaplayer.app.data.api.ApiClient.init(newUrl)
            Toast.makeText(this, "设置已保存", Toast.LENGTH_SHORT).show()
            hideSettingsMenu()
            
            // 如果缓存变了或者服务器变了，立刻重新连接流
            if (allChannels.isNotEmpty() && currentChannelIndex >= 0) {
                playTvChannel(currentChannelIndex)
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
        
        sbSettingsCache?.requestFocus()
    }

    private fun hideSettingsMenu() {
        layoutSettingsMenu?.visibility = View.GONE
        videoLayout?.requestFocus()
    }

    private fun initVlcPlayer() {
        val layout = videoLayout ?: return
        playerHelper = VlcPlayerHelper(this, layout, object : VlcPlayerHelper.PlayerListener {
            override fun onBuffering(percent: Float) {
                if (percent == 100f) {
                    progressBuffering?.visibility = View.GONE
                    retryCount = 0
                } else {
                    progressBuffering?.visibility = View.VISIBLE
                }
            }

            override fun onPlaying(resolution: String) {
                progressBuffering?.visibility = View.GONE
                retryCount = 0
                if (resolution.isNotEmpty()) {
                    tvOsdInfo?.text = resolution
                }
            }

            override fun onError() {
                progressBuffering?.visibility = View.GONE
                
                val channel = allChannels.getOrNull(currentChannelIndex)
                val lines = channel?.getLinesSafely() ?: emptyList()
                
                if (lines.isNotEmpty() && currentLineIndex < lines.size - 1) {
                    currentLineIndex++
                    retryCount = 0
                    Toast.makeText(this@MainActivity, "当前线路失效，切换线路 ${currentLineIndex + 1}...", Toast.LENGTH_SHORT).show()
                    playCurrentLineInTv()
                } else if (retryCount < maxRetries) {
                    retryCount++
                    val delayMs = (3000L * (1 shl (retryCount - 1)))
                    Toast.makeText(this@MainActivity, "播放失败，${delayMs/1000}秒后重试 ($retryCount/$maxRetries)...", Toast.LENGTH_SHORT).show()
                    uiHandler.postDelayed({ 
                        if (allChannels.isNotEmpty()) playCurrentLineInTv()
                    }, delayMs)
                } else {
                    Toast.makeText(this@MainActivity, "当前频道所有线路无法播放", Toast.LENGTH_LONG).show()
                    retryCount = 0
                }
            }
        })
    }

    private fun playTvChannel(index: Int) {
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return
        
        currentCatchupStartTime = null
        currentCatchupChannelIndex = -1
        
        if (currentChannelIndex != index) {
            currentLineIndex = 0
        }
        currentChannelIndex = index
        playCurrentLineInTv()
    }
    
    private fun playCurrentLineInTv() {
        val channel = allChannels.getOrNull(currentChannelIndex) ?: return
        
        tvOsdChannelNum?.text = String.format("%03d", currentChannelIndex + 1)
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

        if (playerHelper == null) {
            initVlcPlayer()
        }
        retryCount = 0
        progressBuffering?.visibility = View.VISIBLE

        playerHelper?.play(line.streamUrl, line.userAgent, line.customHeaders)
        
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
            onClick = { channel, index ->
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
                showAuthWaiting("注册失败: ${e.message}\n\n请检查服务器地址")
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

    private fun showAuthWaiting(message: String) {
        if (isTvMode) {
            tvAuthWaiting?.visibility = View.VISIBLE
            findViewById<TextView>(R.id.tvAuthStatus)?.text = message
        } else {
            phoneAuthWaiting?.visibility = View.VISIBLE
            phoneContent?.visibility = View.GONE
            findViewById<TextView>(R.id.tvAuthStatus)?.text = message
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
            repo.getGroups().onSuccess { list ->
                groups = listOf(ChannelGroup(id = 0, name = "全部")) + list
                groupAdapter.submitList(groups)
                groupAdapter.setSelected(0)
                if (!isTvMode) buildPhoneGroupTabs()
            }

            repo.getChannels().onSuccess { list ->
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
                        if (focus?.id == R.id.btnSettingsSave) {
                            findViewById<View>(R.id.btnSettingsCancel)?.requestFocus()
                            return true
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
                        if (focus?.id == R.id.btnSettingsCancel) {
                            findViewById<View>(R.id.btnSettingsSave)?.requestFocus()
                            return true
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
                        tvChannelsRv?.post {
                            val lm = tvChannelsRv?.layoutManager as? LinearLayoutManager
                            lm?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv?.requestFocus()
                        }
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
        if (isTvMode) {
            // 直播流切后台直接彻底停止，释放硬件解码器和网络连接
            playerHelper?.release()
            playerHelper = null
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        authPollRunnable?.let { authPollHandler.removeCallbacks(it) }
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.removeCallbacks(hideZappingRunnable)
        playerHelper?.release()
        playerHelper = null
    }
}
