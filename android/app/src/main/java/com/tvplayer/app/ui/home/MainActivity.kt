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

    private lateinit var groupAdapter: GroupAdapter
    private lateinit var channelAdapter: ChannelAdapter

    private val authPollHandler = Handler(Looper.getMainLooper())
    private var authPollRunnable: Runnable? = null

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
            override fun onSingleTapConfirmed(e: android.view.MotionEvent): Boolean {
                val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
                if (!isMenuVisible) {
                    layoutZappingMenu?.visibility = View.VISIBLE
                    tvChannelsRv?.requestFocus()
                    uiHandler.removeCallbacks(hideZappingRunnable)
                    uiHandler.postDelayed(hideZappingRunnable, 10000)
                } else {
                    uiHandler.removeCallbacks(hideZappingRunnable)
                    hideZappingRunnable.run()
                }
                return true
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

        tvGroupsRv?.let { FocusHelper.setupTvRecyclerView(it) }
        tvChannelsRv?.let { FocusHelper.setupTvRecyclerView(it) }

        val groupsRv = tvGroupsRv
        val channelsRv = tvChannelsRv
        if (groupsRv != null && channelsRv != null) {
            FocusHelper.linkHorizontalFocus(groupsRv, channelsRv)
        }
    }

    private fun initVlcPlayer() {
        val options = ArrayList<String>()
        options.add("--aout=opensles")
        options.add("--audio-time-stretch")
        options.add("-vvv")
        options.add("--drop-late-frames")
        options.add("--skip-frames")
        options.add("--network-caching=1500")

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
                    if (retryCount < maxRetries) {
                        retryCount++
                        val delayMs = (3000L * (1 shl (retryCount - 1)))
                        Toast.makeText(this@MainActivity, "播放失败，${delayMs/1000}秒后重试 ($retryCount/$maxRetries)...", Toast.LENGTH_SHORT).show()
                        uiHandler.postDelayed({ 
                            if (allChannels.isNotEmpty()) playTvChannel(currentChannelIndex)
                        }, delayMs)
                    } else {
                        Toast.makeText(this@MainActivity, "当前频道无法播放", Toast.LENGTH_LONG).show()
                        retryCount = 0
                    }
                }
            }
        }
    }

    private fun playTvChannel(index: Int) {
        if (allChannels.isEmpty() || index < 0 || index >= allChannels.size) return
        currentChannelIndex = index
        val channel = allChannels[index]
        
        tvOsdChannelNum?.text = String.format("%03d", index + 1)
        tvOsdChannelName?.text = channel.name
        tvOsdInfo?.text = "连接中..."
        
        loadEpgForChannel(channel)
        showOsd()

        val player = mediaPlayer ?: return
        retryCount = 0
        progressBuffering?.visibility = View.VISIBLE

        val media = Media(libVlc, Uri.parse(channel.streamUrl))
        media.setHWDecoderEnabled(true, false)
        player.media = media
        player.play()
        
        // 频道列表中高亮当前播放频道
        channelAdapter.setPlayingIndex(index)
    }
    
    private fun showOsd() {
        layoutOsd?.visibility = View.VISIBLE
        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.postDelayed(hideOsdRunnable, 5000)
    }
    
    private fun loadEpgForChannel(channel: Channel) {
        if (channel.epgChannelId.isNullOrEmpty()) {
            tvOsdEpg?.text = "无节目信息"
            progressEpg?.progress = 0
            return
        }
        lifecycleScope.launch {
            repo.getEPG(channel.epgChannelId).onSuccess { programs ->
                val now = java.util.Date()
                val formats = arrayOf(
                    "yyyy-MM-dd'T'HH:mm:ss", "yyyy-MM-dd'T'HH:mm:ssZ",
                    "yyyy-MM-dd'T'HH:mm:ssXXX", "yyyy-MM-dd HH:mm:ss"
                )
                fun parseDate(s: String): java.util.Date? {
                    for (fmt in formats) {
                        try { return java.text.SimpleDateFormat(fmt, java.util.Locale.getDefault()).parse(s) } catch (_: Exception) {}
                    }
                    return null
                }
                val current = programs.find { p ->
                    val start = parseDate(p.startTime)
                    val end = parseDate(p.endTime)
                    start != null && end != null && now.after(start) && now.before(end)
                }
                if (current != null) {
                    val next = programs.getOrNull(programs.indexOf(current) + 1)
                    val nextText = if (next != null) " | 接下来: ${next.title}" else ""
                    tvOsdEpg?.text = "正在播放: ${current.title}$nextText"
                    
                    val start = parseDate(current.startTime)?.time ?: 0
                    val end = parseDate(current.endTime)?.time ?: 0
                    val currentMs = now.time
                    if (end > start && currentMs > start) {
                        val pct = ((currentMs - start).toFloat() / (end - start).toFloat() * 100).toInt()
                        progressEpg?.progress = max(0, pct)
                    } else {
                        progressEpg?.progress = 0
                    }
                } else {
                    tvOsdEpg?.text = "暂无当前节目信息"
                    progressEpg?.progress = 0
                }
            }
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
                        playTvChannel(0)
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
        val intent = Intent(this, PlayerActivity::class.java).apply {
            putExtra("channel_id", channel.id)
            putExtra("channel_name", channel.name)
            putExtra("stream_url", channel.streamUrl)
            putExtra("stream_type", channel.streamType)
            putExtra("channel_index", currentChannelIndex)
        }
        startActivity(intent)
    }

    // ── TV key events ──────────────────────────────────

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (isTvMode && tvAuthWaiting?.visibility == View.GONE) {
            val isMenuVisible = layoutZappingMenu?.visibility == View.VISIBLE
            when (keyCode) {
                KeyEvent.KEYCODE_MENU, KeyEvent.KEYCODE_DPAD_CENTER, KeyEvent.KEYCODE_ENTER -> {
                    if (!isMenuVisible) {
                        layoutZappingMenu?.visibility = View.VISIBLE
                        tvChannelsRv?.requestFocus()
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        uiHandler.postDelayed(hideZappingRunnable, 10000)
                        return true
                    } else if (keyCode == KeyEvent.KEYCODE_MENU) {
                        startActivity(Intent(this, SettingsActivity::class.java))
                        return true
                    }
                }
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
                    if (!isMenuVisible) {
                        layoutZappingMenu?.visibility = View.VISIBLE
                        tvGroupsRv?.requestFocus()
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        uiHandler.postDelayed(hideZappingRunnable, 10000)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_RIGHT -> {
                    if (isMenuVisible) {
                        uiHandler.removeCallbacks(hideZappingRunnable)
                        hideZappingRunnable.run()
                        return true
                    }
                }
                KeyEvent.KEYCODE_BACK -> {
                    if (isMenuVisible) {
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
            if (isMenuVisible) {
                uiHandler.removeCallbacks(hideZappingRunnable)
                uiHandler.postDelayed(hideZappingRunnable, 10000)
            }
        }
        return super.onKeyDown(keyCode, event)
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
        }
        if (isTvMode) {
            mediaPlayer?.play()
        }
    }
    
    override fun onPause() {
        super.onPause()
        if (isTvMode) {
            mediaPlayer?.pause()
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        authPollHandler.removeCallbacksAndMessages(null)
        uiHandler.removeCallbacksAndMessages(null)
        if (isTvMode) {
            mediaPlayer?.release()
            libVlc?.release()
        }
    }
}
