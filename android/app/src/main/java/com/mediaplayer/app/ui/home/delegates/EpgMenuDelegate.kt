package com.mediaplayer.app.ui.home.delegates

import android.view.View
import android.widget.TextView
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.EPGProgram
import com.mediaplayer.app.ui.home.EpgAdapter
import com.mediaplayer.app.util.FocusHelper
import com.mediaplayer.app.util.EpgCacheManager
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

class EpgMenuDelegate(
    private val layoutEpgMenu: View,
    private val rvEpgList: RecyclerView,
    private val tvEpgMenuTitle: TextView,
    private val tvEpgEmptyText: TextView,
    private val progressEpgLoading: View,
    private val epgAdapter: EpgAdapter,
    private val coroutineScope: CoroutineScope,
    private val onPlayCatchup: (EPGProgram) -> Unit,
    private val fetchEpgData: suspend (String) -> List<EPGProgram>?
) {

    init {
        epgAdapter.setOnItemClickListener { prog ->
            onPlayCatchup(prog)
        }

        rvEpgList.layoutManager = object : LinearLayoutManager(rvEpgList.context) {
            override fun onFocusSearchFailed(focused: View, focusDirection: Int, recycler: RecyclerView.Recycler, state: RecyclerView.State): View? {
                val next = super.onFocusSearchFailed(focused, focusDirection, recycler, state)
                if (next == null && (focusDirection == View.FOCUS_DOWN || focusDirection == View.FOCUS_UP)) {
                    return focused // 捕获焦点
                }
                return next
            }
            override fun requestChildRectangleOnScreen(parent: RecyclerView, child: View, rect: android.graphics.Rect, immediate: Boolean, focusedChildVisible: Boolean): Boolean {
                rect.top -= child.height * 2
                rect.bottom += child.height * 2
                return super.requestChildRectangleOnScreen(parent, child, rect, immediate, focusedChildVisible)
            }
        }

        rvEpgList.setOnKeyListener { _, keyCode, event ->
            if (event.action == android.view.KeyEvent.ACTION_DOWN) {
                if (FocusHelper.trapVerticalScroll(rvEpgList, keyCode)) return@setOnKeyListener true
            }
            false
        }

        rvEpgList.adapter = epgAdapter
    }

    fun isVisible(): Boolean = layoutEpgMenu.visibility == View.VISIBLE

    fun show(channel: Channel, isCurrentCatchupChannel: Boolean, currentCatchupStartTime: String?) {
        layoutEpgMenu.visibility = View.VISIBLE
        tvEpgMenuTitle.text = "节目单"

        val cached = EpgCacheManager.get(channel.name)
        if (cached != null) {
            progressEpgLoading.visibility = View.GONE
            if (cached.isEmpty()) {
                tvEpgEmptyText.visibility = View.VISIBLE
                rvEpgList.visibility = View.GONE
            } else {
                tvEpgEmptyText.visibility = View.GONE
                rvEpgList.visibility = View.VISIBLE
                epgAdapter.setSupportCatchup(channel.supportCatchup)
                if (isCurrentCatchupChannel) {
                    epgAdapter.setActiveProgramStartTime(currentCatchupStartTime)
                } else {
                    epgAdapter.setActiveProgramStartTime(null)
                }
                epgAdapter.setData(cached)
                val pIndex = epgAdapter.getPlayingIndex()
                if (pIndex >= 0) {
                    rvEpgList.scrollToPosition(pIndex)
                    rvEpgList.post {
                        rvEpgList.layoutManager?.findViewByPosition(pIndex)?.requestFocus()
                    }
                } else {
                    rvEpgList.requestFocus()
                }
            }
            return
        }

        rvEpgList.visibility = View.GONE
        tvEpgEmptyText.visibility = View.GONE
        progressEpgLoading.visibility = View.VISIBLE
        rvEpgList.requestFocus()

        coroutineScope.launch(Dispatchers.IO) {
            try {
                val epgId = channel.name
                val programs = fetchEpgData(epgId)
                withContext(Dispatchers.Main) {
                    progressEpgLoading.visibility = View.GONE
                    if (programs != null) {
                        EpgCacheManager.put(channel.name, programs)
                        if (programs.isEmpty()) {
                            tvEpgEmptyText.visibility = View.VISIBLE
                        } else {
                            rvEpgList.visibility = View.VISIBLE
                            epgAdapter.setSupportCatchup(channel.supportCatchup)
                            if (isCurrentCatchupChannel) {
                                epgAdapter.setActiveProgramStartTime(currentCatchupStartTime)
                            } else {
                                epgAdapter.setActiveProgramStartTime(null)
                            }
                            epgAdapter.setData(programs)
                            
                            val pIndex = epgAdapter.getPlayingIndex()
                            if (pIndex >= 0) {
                                rvEpgList.scrollToPosition(pIndex)
                                rvEpgList.post {
                                    rvEpgList.layoutManager?.findViewByPosition(pIndex)?.requestFocus()
                                }
                            }
                        }
                    } else {
                        tvEpgEmptyText.visibility = View.VISIBLE
                    }
                }
            } catch (e: Exception) {
                withContext(Dispatchers.Main) {
                    progressEpgLoading.visibility = View.GONE
                    tvEpgEmptyText.visibility = View.VISIBLE
                }
            }
        }
    }

    fun hide() {
        layoutEpgMenu.visibility = View.GONE
    }
}
