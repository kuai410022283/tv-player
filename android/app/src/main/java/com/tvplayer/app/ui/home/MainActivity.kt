package com.tvplayer.app.ui.home

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
import android.widget.EditText
import android.widget.HorizontalScrollView
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout
import com.tvplayer.app.Prefs
import com.tvplayer.app.R
import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.api.ClientAuthManager
import com.tvplayer.app.data.model.Channel
import com.tvplayer.app.data.model.ChannelGroup
import com.tvplayer.app.data.model.ChannelLine
import com.tvplayer.app.data.repository.ChannelRepository
import com.tvplayer.app.ui.player.PlayerActivity
import com.tvplayer.app.ui.settings.SettingsActivity
import com.tvplayer.app.util.DeviceUtils
import com.tvplayer.app.util.FocusHelper
import kotlinx.coroutines.launch
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.interfaces.IMedia
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
    private var libVlc: LibVLC? = null
    private var mediaPlayer: MediaPlayer? = null
    private var retryCount = 0
    private val maxRetries = 3
    private val uiHandler = Handler(Looper.getMainLooper())
    private val hideOsdRunnable = Runnable { layoutOsd?.visibility = View.GONE }
    private val hideZappingRunnable = Runnable { 
        layoutZappingMenu?.visibility = View.GONE 
        videoLayout?.requestFocus()
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // 强制所有设备使用 TV 的沉浸式界面大一统！
        isTvMode = true

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
                // 长按屏幕直接呼出右侧设置菜单
                showSettingsMenu()
            }

            override fun onFling(e1: android.view.MotionEvent?, e2: android.view.MotionEvent, velocityX: Float, velocityY: Float): Boolean {
                if (e1 == null) return false
                val deltaY = e2.y - e1.y
                val deltaX = e2.x - e1.x
                if (kotlin.math.abs(deltaY) > kotlin.math.abs(deltaX) && kotlin.math.abs(deltaY) > 100) {
                    if (layoutZappingMenu?.visibility == View.VISIBLE) return false
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

        setupSettingsViews()

        tvGroupsRv?.let { FocusHelper.setupTvRecyclerView(it) }
        tvChannelsRv?.let { FocusHelper.setupTvRecyclerView(it) }

        val groupsRv = tvGroupsRv
        val channelsRv = tvChannelsRv
        if (groupsRv != null && channelsRv != null) {
            FocusHelper.linkHorizontalFocus(groupsRv, channelsRv)
        }
    }

    private var rgSettingsDecoder: android.widget.RadioGroup? = null
    private var rgSettingsScale: android.widget.RadioGroup? = null

    private fun setupSettingsViews() {
        val prefs = getSharedPreferences(Prefs.FILE, MODE_PRIVATE)
        val url = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL)
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        val scaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)

        rgSettingsDecoder = findViewById(R.id.rgSettingsDecoder)
        when (decoderMode) {
            Prefs.DECODER_MODE_HARDWARE -> rgSettingsDecoder?.check(R.id.rbDecoderHardware)
            Prefs.DECODER_MODE_SOFTWARE -> rgSettingsDecoder?.check(R.id.rbDecoderSoftware)
            else -> rgSettingsDecoder?.check(R.id.rbDecoderAuto)
        }
        
        rgSettingsScale = findViewById(R.id.rgSettingsScale)
        when (scaleMode) {
            Prefs.SCALE_MODE_STRETCH -> rgSettingsScale?.check(R.id.rbScaleStretch)
            Prefs.SCALE_MODE_CROP -> rgSettingsScale?.check(R.id.rbScaleCrop)
            Prefs.SCALE_MODE_4_3 -> rgSettingsScale?.check(R.id.rbScale43)
            else -> rgSettingsScale?.check(R.id.rbScaleDefault)
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

        btnSettingsCancel?.setOnClickListener {
            hideSettingsMenu()
        }

        btnSettingsSave?.setOnClickListener {
            val newUrl = etSettingsUrl?.text?.toString()?.trim() ?: ""
            val newCacheMs = 500 + (sbSettingsCache?.progress ?: 0) * 100
            
            val newDecoderMode = when (rgSettingsDecoder?.checkedRadioButtonId) {
                R.id.rbDecoderHardware -> Prefs.DECODER_MODE_HARDWARE
                R.id.rbDecoderSoftware -> Prefs.DECODER_MODE_SOFTWARE
                else -> Prefs.DECODER_MODE_AUTO
            }
            
            val newScaleMode = when (rgSettingsScale?.checkedRadioButtonId) {
                R.id.rbScaleStretch -> Prefs.SCALE_MODE_STRETCH
                R.id.rbScaleCrop -> Prefs.SCALE_MODE_CROP
                R.id.rbScale43 -> Prefs.SCALE_MODE_4_3
                else -> Prefs.SCALE_MODE_DEFAULT
            }

            if (newUrl.isEmpty()) {
                Toast.makeText(this, "请输入服务器地址", Toast.LENGTH_SHORT).show()
                return@setOnClickListener
            }

            val oldUrl = prefs.getString(Prefs.KEY_SERVER_URL, "")
            val oldDecoder = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
            val oldScale = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
            if (newUrl != oldUrl || newDecoderMode != oldDecoder) {
                authManager.clearAuth()
                com.tvplayer.app.data.api.ApiClient.reset()
                settingsChanged = true
            }

            prefs.edit()
                .putString(Prefs.KEY_SERVER_URL, newUrl)
                .putInt(Prefs.KEY_NETWORK_CACHE, newCacheMs)
                .putInt(Prefs.KEY_DECODER_MODE, newDecoderMode)
                .putInt(Prefs.KEY_SCALE_MODE, newScaleMode)
                .apply()
                
            com.tvplayer.app.data.api.ApiClient.init(newUrl)
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
        tvSettingsInfo?.text = "应用版本: $versionText\n设备 ID: ${authManager.getDeviceId().take(16)}...\n授权状态: $authStatus"
        
        sbSettingsCache?.requestFocus()
    }

    private fun hideSettingsMenu() {
        layoutSettingsMenu?.visibility = View.GONE
        videoLayout?.requestFocus()
    }

    private fun initVlcPlayer() {
        val prefs = getSharedPreferences(com.tvplayer.app.Prefs.FILE, MODE_PRIVATE)
        val cacheMs = prefs.getInt(com.tvplayer.app.Prefs.KEY_NETWORK_CACHE, com.tvplayer.app.Prefs.DEFAULT_NETWORK_CACHE)

        val options = ArrayList<String>()
        options.add("--aout=opensles")
        options.add("--audio-time-stretch")
        options.add("-vvv")
        options.add("--drop-late-frames")
        options.add("--skip-frames")
        options.add("--network-caching=$cacheMs")

        libVlc = LibVLC(this, options)
        mediaPlayer = MediaPlayer(libVlc)
        videoLayout?.let { mediaPlayer?.attachViews(it, null, false, false) }

        mediaPlayer?.setEventListener { event ->
            when (event.type) {
                MediaPlayer.Event.Buffering -> {
                    if (event.buffering == 100f) {
                        progressBuffering?.visibility = View.GONE
                        retryCount = 0
                    } else {
                        progressBuffering?.visibility = View.VISIBLE
                    }
                }
                MediaPlayer.Event.Playing -> {
                    progressBuffering?.visibility = View.GONE
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
                        tvOsdInfo?.text = info
                    }
                }
                MediaPlayer.Event.EncounteredError -> {
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
            }
        }
    }

    private fun playTvChannel(index: Int) {
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return
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

        val player = mediaPlayer ?: return
        retryCount = 0
        progressBuffering?.visibility = View.VISIBLE

        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        val scaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        
        when (scaleMode) {
            Prefs.SCALE_MODE_STRETCH -> {
                player.aspectRatio = "16:9"
            }
            Prefs.SCALE_MODE_CROP -> {
                player.aspectRatio = null
            }
            Prefs.SCALE_MODE_4_3 -> {
                player.aspectRatio = "4:3"
            }
            else -> {
                player.aspectRatio = null
            }
        }
        
        val media = Media(libVlc, Uri.parse(line.streamUrl))
        media.setHWDecoderEnabled(true, false)
        media.addOption(":network-caching=$cacheMs")
        if (scaleMode == Prefs.SCALE_MODE_CROP) {
            media.addOption(":crop=16:9")
        }
        applyMediaOptions(media, line.userAgent, line.customHeaders)
        player.media = media
        player.play()
        
        // 频道列表中高亮当前播放频道
        channelAdapter.setPlayingIndex(currentChannelIndex)
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
    
    private fun showOsd() {
        layoutOsd?.visibility = View.VISIBLE
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.postDelayed(hideOsdRunnable, 5000)
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
                authManager.verify().onSuccess { valid ->
                    if (valid) showContent() else doRegister()
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
                    "approved" -> showContent()
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
                        if (status == "approved") { showContent(); return@launch }
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
        loadData()
        startHeartbeat()
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

            // 当菜单未显示时，开始追踪 OK 键的长按事件
            if (!isMenuVisible && !isSettingsVisible && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                event?.startTracking()
                return true
            }

            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> {
                    if (!isMenuVisible) {
                        val prev = if (currentChannelIndex > 0) currentChannelIndex - 1 else allChannels.size - 1
                        playTvChannel(prev)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (!isMenuVisible) {
                        val next = if (currentChannelIndex < allChannels.size - 1) currentChannelIndex + 1 else 0
                        playTvChannel(next)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_LEFT -> {
                    if (isSettingsVisible) {
                        return true // 防止设置面板里左方向键触发别的
                    }
                    if (!isMenuVisible) {
                        layoutZappingMenu?.visibility = View.VISIBLE
                        tvGroupsRv?.requestFocus()
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        uiHandler.postDelayed(hideZappingRunnable, 10000)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_RIGHT -> {
                    if (isSettingsVisible) {
                        return true
                    }
                    if (isMenuVisible) {
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        hideZappingRunnable.run()
                        return true
                    }
                }
                KeyEvent.KEYCODE_BACK -> {
                    if (isSettingsVisible) {
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
            // 长按 OK 键呼出右侧设置
            showSettingsMenu()
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
            mediaPlayer?.stop()
            mediaPlayer?.vlcVout?.detachViews()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        authPollRunnable?.let { authPollHandler.removeCallbacks(it) }
        heartbeatRunnable?.let { heartbeatHandler.removeCallbacks(it) }
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.removeCallbacks(hideZappingRunnable)
        mediaPlayer?.release()
        libVlc?.release()
    }
}
