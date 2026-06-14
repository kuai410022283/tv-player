package com.mediaplayer.app.ui.home.delegates

import android.view.View
import android.widget.TextView
import android.widget.Toast
import android.widget.ImageView

import kotlinx.coroutines.launch
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.data.model.Channel
import androidx.recyclerview.widget.LinearLayoutManager
import com.mediaplayer.app.ui.home.GroupAdapter
import com.mediaplayer.app.ui.home.ChannelAdapter
import com.mediaplayer.app.data.model.ChannelGroup
import androidx.lifecycle.lifecycleScope
import com.mediaplayer.app.ui.home.MainActivity

class AppLifecycleDelegate(private val activity: MainActivity) {

    fun setupQrConfigServer(onUrlUpdated: () -> Unit) {
        if (activity.configWebServer == null) {
            activity.configWebServer = com.mediaplayer.app.server.ConfigWebServer(activity, 9528) { rawUrl ->
                activity.runOnUiThread {
                    val newUrl = com.mediaplayer.app.data.api.ApiClient.formatUrl(rawUrl)
                    val prefs = activity.getSharedPreferences(Prefs.FILE, android.content.Context.MODE_PRIVATE)
                    prefs.edit().putString(Prefs.KEY_SERVER_URL, newUrl).apply()
                    com.mediaplayer.app.data.api.ApiClient.init(newUrl)
                    activity.authManager.clearAuth()
                    onUrlUpdated()
                }
            }
            try {
                activity.configWebServer?.start()
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
    }

    fun handleAuthSuccess(sysAnnouncement: String?, sysAnnouncementInterval: Int, startupMediaEnabled: Boolean, startupMediaUrl: String?, startupMediaType: String, startupDuration: Int, startupSkipAfter: Int) {
        this.activity.sysAnnouncement = sysAnnouncement
        this.activity.sysAnnouncementInterval = sysAnnouncementInterval
        
        if (startupMediaEnabled && !startupMediaUrl.isNullOrEmpty() && !activity.hasShownSplash) {
            activity.hasShownSplash = true
            val intent = android.content.Intent(activity, com.mediaplayer.app.ui.splash.SplashMediaActivity::class.java).apply {
                putExtra(com.mediaplayer.app.ui.splash.SplashMediaActivity.EXTRA_MEDIA_URL, startupMediaUrl)
                putExtra(com.mediaplayer.app.ui.splash.SplashMediaActivity.EXTRA_MEDIA_TYPE, startupMediaType)
                putExtra(com.mediaplayer.app.ui.splash.SplashMediaActivity.EXTRA_DURATION, startupDuration)
                putExtra(com.mediaplayer.app.ui.splash.SplashMediaActivity.EXTRA_SKIP_AFTER, startupSkipAfter)
            }
            activity.startActivityForResult(intent, 1001)
        } else {
            showContent()
        }
    }

    fun checkAuthAndLoad() {
        activity.lifecycleScope.launch {
            if (activity.authManager.isApproved()) {
                activity.authManager.verify().onSuccess { resp ->
                    if (resp != null) {
                        handleAuthSuccess(resp.announcement, resp.announcementInterval, resp.startupMediaEnabled, resp.startupMedia, resp.startupMediaType, resp.startupDuration, resp.startupSkipAfter)
                    } else doRegister()
                }.onFailure { doRegister() }
            } else {
                doRegister()
            }
        }
    }

    fun doRegister() {
        activity.lifecycleScope.launch {
            showAuthWaiting("正在注册设备...")
            activity.authManager.register().onSuccess { result ->
                when (result.status) {
                    "approved" -> {
                        handleAuthSuccess(result.announcement, result.announcementInterval, result.startupMediaEnabled, result.startupMedia, result.startupMediaType, result.startupDuration, result.startupSkipAfter)
                    }
                    "pending" -> {
                        showAuthWaiting("设备已注册，等待管理员审批...\n\n设备ID: ${activity.authManager.getDeviceId()}")
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

    fun startAuthPolling() {
        val runnable = object : Runnable {
            override fun run() {
                activity.lifecycleScope.launch {
                    activity.authManager.checkStatus().onSuccess { status ->
                        if (status == "approved") { 
                            activity.authManager.verify().onSuccess { resp ->
                                if (resp != null) {
                                    activity.sysAnnouncement = resp.announcement
                                    activity.sysAnnouncementInterval = resp.announcementInterval
                                }
                                showContent()
                            }
                            return@launch 
                        }
                        activity.authPollRunnable?.let { activity.authPollHandler.postDelayed(it, 10000) }
                    }.onFailure { activity.authPollRunnable?.let { activity.authPollHandler.postDelayed(it, 15000) } }
                }
            }
        }
        activity.authPollRunnable = runnable
        activity.authPollHandler.postDelayed(runnable, 10000)
    }

    fun showAuthWaiting(message: String, showQr: Boolean = false) {
        if (activity.isTvMode) {
            activity.findViewById<View>(R.id.layoutAuthWaiting)?.visibility = View.VISIBLE
            activity.findViewById<TextView>(R.id.tvAuthStatus)?.text = message
        }
        
        if (showQr) {
            val ip = com.mediaplayer.app.util.NetworkUtils.getLocalIpAddress()
            if (ip != null) {
                setupQrConfigServer {
                    Toast.makeText(activity, "配置已保存，正在重试...", Toast.LENGTH_LONG).show()
                    checkAuthAndLoad()
                }
                val qrUrl = "http://$ip:9528/"
                val bitmap = com.mediaplayer.app.util.QRCodeHelper.generateQRCode(qrUrl, 400)
                activity.findViewById<ImageView>(R.id.ivAuthQrCode)?.setImageBitmap(bitmap)
                activity.findViewById<TextView>(R.id.tvAuthQrConfigHint)?.text = "手机扫码设置服务器\n或访问: $qrUrl"
                activity.findViewById<TextView>(R.id.tvAuthQrConfigHint)?.setOnClickListener {
                    try {
                        val intent = android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(qrUrl))
                        intent.addFlags(android.content.Intent.FLAG_ACTIVITY_NEW_TASK)
                        activity.startActivity(intent)
                    } catch (e: Exception) {
                        e.printStackTrace()
                    }
                }
                activity.findViewById<View>(R.id.layoutAuthQrConfig)?.visibility = View.VISIBLE
            }
        } else {
            activity.findViewById<View>(R.id.layoutAuthQrConfig)?.visibility = View.GONE
        }
    }

    fun showContent() {
        activity.findViewById<View>(R.id.layoutAuthWaiting)?.visibility = View.GONE

        // 初始触发跑马灯
        if (!activity.sysAnnouncement.isNullOrEmpty()) {
            triggerMarquee()
        }

        loadData()
        startHeartbeat()
    }

    fun triggerMarquee() {
        activity.uiHandler.removeCallbacks(activity.marqueeRunnable)
        activity.uiHandler.removeCallbacks(activity.hideMarqueeRunnable)
        
        val layoutAnnouncement = activity.findViewById<android.view.View>(R.id.layoutAnnouncement) ?: return
        val tvAnnouncement = activity.findViewById<android.widget.TextView>(R.id.tvAnnouncement) ?: return
        
        if (activity.sysAnnouncement.isNullOrEmpty()) {
            layoutAnnouncement.visibility = View.GONE
            activity.marqueeIsVisible = false
            return
        }

        layoutAnnouncement.visibility = View.VISIBLE
        activity.marqueeIsVisible = true
        
        // 强制原生跑马灯滚动：如果文字太短，补齐空格直到超过屏幕宽度
        var text = activity.sysAnnouncement!!
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
            val density = activity.resources.displayMetrics.density
            val speedPxPerSec = 30f * density
            // 跑完一整圈需要走过的总距离 = 屏幕宽度 + 文字总长度
            val requiredTimeMs = ((screenWidth + textWidth) / speedPxPerSec * 1000f).toLong()
            
            // 取 25 秒和实际需要时间中的最大值，确保哪怕是超长文本也能至少被完整看完一遍
            val displayDuration = maxOf(25000L, requiredTimeMs)
            
            // 原生跑马灯运行完毕后自动隐藏，并进入下一轮间隔排期
            activity.uiHandler.postDelayed(activity.hideMarqueeRunnable, displayDuration)
        }
    }

    fun startHeartbeat() {
        activity.heartbeatRunnable?.let { activity.heartbeatHandler.removeCallbacks(it) }
        val runnable = object : Runnable {
            override fun run() {
                activity.lifecycleScope.launch {
                    try {
                        activity.authManager.verify().onSuccess { resp ->
                            if (resp != null && !resp.announcement.isNullOrEmpty() && resp.announcement != activity.sysAnnouncement) {
                                activity.sysAnnouncement = resp.announcement
                                activity.sysAnnouncementInterval = resp.announcementInterval
                                triggerMarquee()
                            }
                        }
                    } catch (_: Exception) {}
                }
                activity.heartbeatHandler.postDelayed(this, 3 * 60 * 1000) // 每3分钟心跳
            }
        }
        activity.heartbeatRunnable = runnable
        activity.heartbeatHandler.postDelayed(runnable, 3 * 60 * 1000)
    }

    fun loadData() {
        if (activity.isLoadingData) return
        activity.isLoadingData = true
        activity.lifecycleScope.launch {
            try {
                // 1. 先拉分组列表
                val realGroups = activity.repo.getGroups().getOrElse { emptyList() }
                val groups = listOf(ChannelGroup(id = 0, name = "全部")) + realGroups
                activity.groupAdapter.submitList(groups)
                activity.groupAdapter.setSelected(0)

                // 2. 按分组并行拉取全量频道（彻底绕过全局 page_size 上限）
                activity.repo.getAllChannelsByGroups(realGroups).onSuccess { list ->
                    list.forEachIndexed { index, channel ->
                        channel.globalIndex = index
                    }
                    activity.allChannels = list
                    activity.channelsByGroup = list.groupBy { it.groupId }
                    if (list.isNotEmpty()) {
                        // 尝试恢复上次播放的频道
                        val prefs = activity.getSharedPreferences(Prefs.FILE, android.content.Context.MODE_PRIVATE)
                        val lastChannelId = prefs.getLong("last_channel_id", -1L)
                        var targetIndex = 0
                        if (lastChannelId != -1L) {
                            val foundIndex = list.indexOfFirst { it.id == lastChannelId }
                            if (foundIndex != -1) {
                                targetIndex = foundIndex
                            }
                        }
                        activity.currentGroupId = list[targetIndex].groupId
                        activity.groupAdapter.setSelected(activity.currentGroupId)
                        filterChannels(scrollToTop = false)
                        activity.playbackLifecycleDelegate.playTvChannel(targetIndex)
                        activity.videoLayout?.requestFocus()
                    }
                }.onFailure {
                    // handle failure
                }
            } finally {
                activity.isLoadingData = false
            }
        }
    }

    fun filterChannels(scrollToTop: Boolean = true) {
        activity.filteredChannels = if (activity.currentGroupId == 0L) {
            activity.allChannels
        } else {
            activity.channelsByGroup[activity.currentGroupId] ?: emptyList()
        }
        activity.channelAdapter.setData(activity.filteredChannels)
        
        if (scrollToTop) {
            activity.tvChannelsRv?.post {
                activity.tvChannelsRv?.scrollToPosition(0) // 分组切换时，频道列表重置到顶部
            }
        }
    }

    fun setupAdapters() {
        activity.groupAdapter = GroupAdapter(
            onClick = { group ->
                activity.currentGroupId = group.id
                filterChannels(scrollToTop = true)
                activity.groupAdapter.setSelected(group.id)
                activity.tvChannelsRv?.requestFocus()
            },
            onFocus = { group ->
                android.util.Log.d("TV_FOCUS", "MainActivity received onFocus for: ${group.name}, activity.isTvMode: $activity.isTvMode")
                if (activity.isTvMode) {
                    // 立即更新左侧分组的选中UI状态，不要有延迟
                    activity.currentGroupId = group.id
                    activity.groupAdapter.setSelected(group.id)
                    
                    activity.focusDebounceRunnable?.let { activity.focusDebounceHandler.removeCallbacks(it) }
                    val r = Runnable {
                        android.util.Log.d("TV_FOCUS", "Executing runnable for: ${group.name}")
                        filterChannels(scrollToTop = true)
                    }
                    activity.focusDebounceRunnable = r
                    activity.focusDebounceHandler.postDelayed(r, 150)
                }
            }
        )

        activity.channelAdapter = ChannelAdapter(
            isTvMode = activity.isTvMode,
            onClick = { channel, _ ->
                    val realIndex = activity.allChannels.indexOf(channel)
                    activity.playbackLifecycleDelegate.playTvChannel(realIndex)
                    activity.uiHandler.postDelayed(activity.hideZappingRunnable, 500)
            }
        )
        
        val prefs = activity.getSharedPreferences(Prefs.FILE, android.content.Context.MODE_PRIVATE)
        activity.channelAdapter.showLogo = prefs.getBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, true)

        if (activity.isTvMode) {
            activity.tvGroupsRv?.apply {
                layoutManager = object : LinearLayoutManager(activity) {
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
                adapter = activity.groupAdapter
            }
            activity.tvChannelsRv?.apply {
                setHasFixedSize(true)
                layoutManager = object : LinearLayoutManager(activity) {
                    override fun onFocusSearchFailed(focused: View, focusDirection: Int, recycler: androidx.recyclerview.widget.RecyclerView.Recycler, state: androidx.recyclerview.widget.RecyclerView.State): View? {
                        val next = super.onFocusSearchFailed(focused, focusDirection, recycler, state)
                        if (next == null && (focusDirection == View.FOCUS_DOWN || focusDirection == View.FOCUS_UP)) {
                            return focused // 捕获焦点，防止快速滚动时意外逃逸到侧边栏
                        }
                        return next
                    }
                    override fun onInterceptFocusSearch(focused: View, direction: Int): View? {
                        if (direction == View.FOCUS_LEFT) {
                            val groupIndex = activity.groupAdapter.currentList.indexOfFirst { it.id == activity.currentGroupId }
                            if (groupIndex >= 0) {
                                val groupLm = activity.tvGroupsRv?.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                                val targetView = groupLm?.findViewByPosition(groupIndex)
                                if (targetView == null) {
                                    // 补丁A: 当目标卡片因为滚动而处于屏幕外时，强制列表先 scrollToPosition
                                    activity.tvGroupsRv?.scrollToPosition(groupIndex)
                                    activity.tvGroupsRv?.post {
                                        val newLm = activity.tvGroupsRv?.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                                        newLm?.findViewByPosition(groupIndex)?.requestFocus()
                                    }
                                }
                                return targetView ?: activity.tvGroupsRv
                            }
                        }
                        return super.onInterceptFocusSearch(focused, direction)
                    }
                    override fun requestChildRectangleOnScreen(parent: androidx.recyclerview.widget.RecyclerView, child: View, rect: android.graphics.Rect, immediate: Boolean, focusedChildVisible: Boolean): Boolean {
                        rect.top -= child.height * 2
                        rect.bottom += child.height * 2
                        return super.requestChildRectangleOnScreen(parent, child, rect, immediate, focusedChildVisible)
                    }
                }
                adapter = activity.channelAdapter
            }
        }
    }

}
