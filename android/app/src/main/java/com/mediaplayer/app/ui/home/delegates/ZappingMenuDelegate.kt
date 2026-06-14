package com.mediaplayer.app.ui.home.delegates

import android.view.View
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.mediaplayer.app.ui.home.ChannelAdapter
import com.mediaplayer.app.ui.home.GroupAdapter
import com.mediaplayer.app.util.FocusHelper

class ZappingMenuDelegate(
    private val layoutZappingMenu: View,
    private val tvGroupsRv: RecyclerView,
    private val tvChannelsRv: RecyclerView,
    private val groupAdapter: GroupAdapter,
    private val channelAdapter: ChannelAdapter,
    private val getCurrentGroupId: () -> Long
) {
    // The RecyclerView layoutManager and adapter are initialized by AppLifecycleDelegate


    val groupsRv: RecyclerView get() = tvGroupsRv
    val channelsRv: RecyclerView get() = tvChannelsRv

    fun isVisible(): Boolean = layoutZappingMenu.visibility == View.VISIBLE

    fun show(activeListArea: String, currentGroupId: Long) {
        // 先封锁分组列表的焦点，防止可见性改变时系统自动分配焦点导致跳组
        tvGroupsRv.descendantFocusability = android.view.ViewGroup.FOCUS_BLOCK_DESCENDANTS
        layoutZappingMenu.visibility = View.VISIBLE

        // 恢复焦点
        if (activeListArea == "groups") {
            tvGroupsRv.descendantFocusability = android.view.ViewGroup.FOCUS_AFTER_DESCENDANTS
            val groupIndex = groupAdapter.currentList.indexOfFirst { it.id == currentGroupId }
            if (groupIndex != -1) {
                tvGroupsRv.scrollToPosition(groupIndex)
                tvGroupsRv.post {
                    val lm = tvGroupsRv.layoutManager as? LinearLayoutManager
                    lm?.findViewByPosition(groupIndex)?.requestFocus() ?: tvGroupsRv.requestFocus()
                }
            } else {
                tvGroupsRv.requestFocus()
            }
        } else {
            // channels
            val playingId = channelAdapter.getPlayingChannelId()
            val indexInFiltered = channelAdapter.getChannels().indexOfFirst { it.id == playingId }
            if (indexInFiltered != -1) {
                tvChannelsRv.scrollToPosition(indexInFiltered)
            }
            tvChannelsRv.postDelayed({
                tvGroupsRv.descendantFocusability = android.view.ViewGroup.FOCUS_AFTER_DESCENDANTS
                if (indexInFiltered != -1) {
                    val lm = tvChannelsRv.layoutManager as? LinearLayoutManager
                    val view = lm?.findViewByPosition(indexInFiltered)
                    if (view != null) {
                        view.requestFocus()
                    } else {
                        tvChannelsRv.post {
                            val lm2 = tvChannelsRv.layoutManager as? LinearLayoutManager
                            lm2?.findViewByPosition(indexInFiltered)?.requestFocus() ?: tvChannelsRv.requestFocus()
                        }
                    }
                } else {
                    val lm = tvChannelsRv.layoutManager as? LinearLayoutManager
                    val firstVisible = lm?.findFirstVisibleItemPosition() ?: 0
                    lm?.findViewByPosition(firstVisible)?.requestFocus() ?: tvChannelsRv.requestFocus()
                }
            }, 100)
        }
    }

    fun hide() {
        layoutZappingMenu.visibility = View.GONE
    }

    fun blockGroupFocusDescendants() {
        tvGroupsRv.descendantFocusability = android.view.ViewGroup.FOCUS_BLOCK_DESCENDANTS
    }
}
