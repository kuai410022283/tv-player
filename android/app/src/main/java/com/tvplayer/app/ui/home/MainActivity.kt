package com.tvplayer.app.ui.home

import android.content.Intent
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
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
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

class MainActivity : AppCompatActivity() {

    private val repo = ChannelRepository()
    private lateinit var authManager: ClientAuthManager
    private var isTvMode = false

    // ── Views (TV mode) ──
    private var tvGroupsRv: RecyclerView? = null
    private var tvChannelsRv: RecyclerView? = null
    private var tvChannelCount: TextView? = null
    private var tvAuthWaiting: View? = null
    private var tvContent: View? = null

    // ── Views (Phone mode) ──
    private var phoneGroupTabs: LinearLayout? = null
    private var phoneChannelsRv: RecyclerView? = null
    private var phoneChannelCount: TextView? = null
    private var phoneAuthWaiting: View? = null
    private var phoneContent: View? = null
    private var phoneSearchLayout: View? = null
    private var phoneSearchEdit: EditText? = null
    private var phoneScrollView: HorizontalScrollView? = null

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

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        isTvMode = DeviceUtils.isTV(this)

        if (isTvMode) {
            setContentView(R.layout.activity_main)
            setupTvViews()
        } else {
            setContentView(R.layout.activity_main_phone)
            setupPhoneViews()
        }

        authManager = ClientAuthManager(this)

        val prefs = getSharedPreferences("tvplayer", MODE_PRIVATE)
        val serverUrl = prefs.getString("server_url", "http://10.0.2.2:9527") ?: "http://10.0.2.2:9527"
        ApiClient.init(serverUrl)

        setupAdapters()
        checkAuthAndLoad()
    }

    // ═══════════════════════════════════════════════════
    // TV MODE SETUP
    // ═══════════════════════════════════════════════════

    private fun setupTvViews() {
        tvGroupsRv = findViewById(R.id.rvGroups)
        tvChannelsRv = findViewById(R.id.rvChannels)
        tvChannelCount = findViewById(R.id.tvChannelCount)
        tvAuthWaiting = findViewById(R.id.layoutAuthWaiting)
        tvContent = findViewById(R.id.layoutContent)

        // TV 焦点优化
        tvGroupsRv?.let { FocusHelper.setupTvRecyclerView(it) }
        tvChannelsRv?.let { FocusHelper.setupTvRecyclerView(it) }

        // 左右列表焦点联动
        if (tvGroupsRv != null && tvChannelsRv != null) {
            FocusHelper.linkHorizontalFocus(tvGroupsRv!!, tvChannelsRv!!)
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
        phoneScrollView = findViewById(R.id.layoutGroupTabs)?.parent as? HorizontalScrollView

        // 搜索按钮
        findViewById<View>(R.id.btnSearch)?.setOnClickListener {
            toggleSearch()
        }

        // 设置按钮
        findViewById<View>(R.id.btnSettings)?.setOnClickListener {
            startActivity(Intent(this, SettingsActivity::class.java))
        }

        // 搜索输入
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
        updateChannelCount()
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
                currentChannelIndex = index
                playChannel(channel)
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
        authPollRunnable = object : Runnable {
            override fun run() {
                lifecycleScope.launch {
                    authManager.checkStatus().onSuccess { status ->
                        if (status == "approved") { showContent(); return@launch }
                        authPollHandler.postDelayed(this@Runnable, 10000)
                    }.onFailure { authPollHandler.postDelayed(this@Runnable, 15000) }
                }
            }
        }
        authPollHandler.postDelayed(authPollRunnable!!, 10000)
    }

    private fun showAuthWaiting(message: String) {
        if (isTvMode) {
            tvAuthWaiting?.visibility = View.VISIBLE
            tvContent?.visibility = View.GONE
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
            tvContent?.visibility = View.VISIBLE
        } else {
            phoneAuthWaiting?.visibility = View.GONE
            phoneContent?.visibility = View.VISIBLE
        }
        loadData()
    }

    private fun loadData() {
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
                updateChannelCount()
                if (list.isNotEmpty()) {
                    currentChannelIndex = 0
                    if (isTvMode) {
                        tvChannelsRv?.requestFocus()
                    }
                }
            }
        }
    }

    private fun filterChannels() {
        filteredChannels = if (currentGroupId == 0L) {
            allChannels
        } else {
            allChannels.filter { it.groupId == currentGroupId }
        }
        channelAdapter.submitList(filteredChannels)
        updateChannelCount()
    }

    private fun updateChannelCount() {
        val text = "${filteredChannels.size} 个频道"
        if (isTvMode) tvChannelCount?.text = text else phoneChannelCount?.text = text
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

    // ── Play channel ───────────────────────────────────

    private fun playChannel(channel: Channel) {
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
        if (isTvMode) {
            when (keyCode) {
                KeyEvent.KEYCODE_MENU -> {
                    startActivity(Intent(this, SettingsActivity::class.java))
                    return true
                }
            }
        }
        return super.onKeyDown(keyCode, event)
    }

    override fun onResume() {
        super.onResume()
        loadData()
    }

    override fun onDestroy() {
        super.onDestroy()
        authPollHandler.removeCallbacksAndMessages(null)
    }
}
