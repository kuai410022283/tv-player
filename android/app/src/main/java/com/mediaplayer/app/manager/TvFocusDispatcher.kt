package com.mediaplayer.app.manager

import android.view.View
import android.view.ViewTreeObserver
import android.view.Window
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.mediaplayer.app.util.RemoteLogger

class TvFocusDispatcher {

    private var isBouncingFocus = false
    private var globalFocusListener: ViewTreeObserver.OnGlobalFocusChangeListener? = null
    private var window: Window? = null
    private var getActiveArea: (() -> String)? = null

    fun attach(
        window: Window,
        tvGroupsRv: RecyclerView?,
        tvChannelsRv: RecyclerView?,
        rvEpgList: RecyclerView?,
        getActiveArea: () -> String
    ) {
        this.window = window
        this.getActiveArea = getActiveArea
        globalFocusListener = ViewTreeObserver.OnGlobalFocusChangeListener { _, newFocus ->
            if (newFocus != null) {
                val activeListArea = getActiveArea()
                val isNewGroups = isViewDescendantOf(newFocus, tvGroupsRv)
                val isNewChannels = isViewDescendantOf(newFocus, tvChannelsRv)
                val isNewEpg = isViewDescendantOf(newFocus, rvEpgList)

                if (isNewGroups && activeListArea != "groups") {
                    RemoteLogger.i("FocusTrace", "Involuntary jump to Groups rejected! Forcing back to \$activeListArea.")
                    bounceFocusBack(activeListArea, tvGroupsRv, tvChannelsRv, rvEpgList)
                } else if (isNewChannels && activeListArea != "channels") {
                    RemoteLogger.i("FocusTrace", "Involuntary jump to Channels rejected! Forcing back to \$activeListArea.")
                    bounceFocusBack(activeListArea, tvGroupsRv, tvChannelsRv, rvEpgList)
                } else if (isNewEpg && activeListArea != "epg") {
                    RemoteLogger.i("FocusTrace", "Involuntary jump to EPG rejected! Forcing back to \$activeListArea.")
                    bounceFocusBack(activeListArea, tvGroupsRv, tvChannelsRv, rvEpgList)
                }
            }
        }
        window.decorView.viewTreeObserver.addOnGlobalFocusChangeListener(globalFocusListener)
    }

    fun detach() {
        globalFocusListener?.let {
            window?.decorView?.viewTreeObserver?.removeOnGlobalFocusChangeListener(it)
        }
        globalFocusListener = null
        window = null
        getActiveArea = null
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

    private fun bounceFocusBack(activeListArea: String, tvGroupsRv: RecyclerView?, tvChannelsRv: RecyclerView?, rvEpgList: RecyclerView?) {
        if (isBouncingFocus) return
        isBouncingFocus = true

        when (activeListArea) {
            "channels" -> {
                val rv = tvChannelsRv
                val lm = rv?.layoutManager as? LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
            "groups" -> {
                val rv = tvGroupsRv
                val lm = rv?.layoutManager as? LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
            "epg" -> {
                val rv = rvEpgList
                val lm = rv?.layoutManager as? LinearLayoutManager
                val pos = lm?.findFirstCompletelyVisibleItemPosition()?.takeIf { it != -1 } ?: lm?.findFirstVisibleItemPosition() ?: 0
                if (pos != -1) {
                    val view = lm?.findViewByPosition(pos)
                    if (view != null) view.requestFocus() else rv?.requestFocus()
                }
            }
        }

        // Delay resetting the flag slightly to prevent cascading focus loops
        tvChannelsRv?.postDelayed({ isBouncingFocus = false }, 50)
    }
}
