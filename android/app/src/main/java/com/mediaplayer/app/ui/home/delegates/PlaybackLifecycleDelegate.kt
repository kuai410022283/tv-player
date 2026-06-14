package com.mediaplayer.app.ui.home.delegates

import android.content.Context
import android.net.Uri
import android.view.View
import android.widget.TextView
import android.widget.ProgressBar
import android.widget.Toast
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelLine
import com.mediaplayer.app.util.VlcPlayerHelper
import org.videolan.libvlc.util.VLCVideoLayout
import com.mediaplayer.app.ui.home.MainActivity

class PlaybackLifecycleDelegate(private val activity: MainActivity) {

    fun initPlayerWithCore(core: Int) { }

    fun playTvChannel(index: Int, isAutoSkip: Boolean = false) {
        if (activity.allChannels.isEmpty() || index < 0 || index >= activity.allChannels.size) return
        activity.currentChannelIndex = index
        val channel = activity.allChannels[index]
        loadEpgForChannel(channel)
        activity.playbackSessionManager.playChannel(channel, isAutoSkip)
        activity.channelAdapter.setPlayingChannelId(channel.id)
    }

    fun loadEpgForChannel(channel: Channel) {
        if (channel.currentEpg.isNotEmpty()) {
            activity.findViewById<TextView>(R.id.tvOsdEpg)?.text = "正在播放: ${channel.currentEpg}"
            activity.findViewById<android.widget.ProgressBar>(R.id.progressEpg)?.progress = channel.epgPercent
        } else {
            activity.findViewById<TextView>(R.id.tvOsdEpg)?.text = "暂无当前节目信息"
            activity.findViewById<android.widget.ProgressBar>(R.id.progressEpg)?.progress = 0
        }
    }

}
