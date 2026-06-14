package com.mediaplayer.app.ui.home.delegates

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.ImageView
import android.widget.TextView
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.util.IPlayerHelper

class PlayerOverlayDelegate(
    private val layoutOsd: View,
    private val tvOsdChannelNum: TextView,
    private val tvOsdChannelName: TextView,
    private val tvOsdInfo: TextView,
    private val tvOsdEpg: TextView,
    private val layoutSettingsMenu: View,
    private val layoutLineMenu: View
    
    
) {
    private val uiHandler = Handler(Looper.getMainLooper())
    private val hideOsdRunnable = Runnable { hideOsd() }

    init {
        
    }

    fun isOsdVisible(): Boolean = layoutOsd.visibility == View.VISIBLE
    fun isSettingsVisible(): Boolean = layoutSettingsMenu.visibility == View.VISIBLE
    fun isLineSelectionVisible(): Boolean = layoutLineMenu.visibility == View.VISIBLE

    fun showOsd(channel: Channel?, playerHelper: IPlayerHelper?) {
        layoutOsd.visibility = View.VISIBLE
        if (channel != null) {
            tvOsdChannelNum.text = String.format("%03d", channel.globalIndex + 1)
            tvOsdChannelName.text = channel.name
            
            if (channel.currentEpg.isNotEmpty()) {
                tvOsdEpg.text = "正在播放: ${channel.currentEpg}"
            } else {
                tvOsdEpg.text = "暂无当前节目信息"
            }
            val progressEpg = layoutOsd.rootView.findViewById<android.widget.ProgressBar>(com.mediaplayer.app.R.id.progressEpg)
            progressEpg?.progress = channel.epgPercent
        }
        
        if (playerHelper?.isPlaying() == true) {
            val infoText = tvOsdInfo.text?.toString() ?: ""
            if (infoText.contains("连接中") || infoText.isEmpty()) {
                val context = layoutOsd.context
                val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
                val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
                val decoderStr = when (decoderMode) {
                    Prefs.DECODER_MODE_HARDWARE -> "硬解"
                    Prefs.DECODER_MODE_SOFTWARE -> "软解"
                    else -> "自动解码"
                }
                val coreStr = playerHelper.let {
                    when (it) {
                        is com.mediaplayer.app.util.ExoPlayerHelper -> "ExoPlayer"
                        is com.mediaplayer.app.util.IjkPlayerHelper -> "IJKPlayer"
                        is com.mediaplayer.app.util.VlcPlayerHelper -> "VLC"
                        else -> ""
                    }
                }
                tvOsdInfo.text = if (coreStr.isNotEmpty()) "$decoderStr | $coreStr" else decoderStr
            }
        }

        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.postDelayed(hideOsdRunnable, 5000)
    }

    fun hideOsd() {
        layoutOsd.visibility = View.GONE
    }

    fun setOsdInfo(text: String) {
        tvOsdInfo.text = text
    }

    fun showSettings() {
        layoutSettingsMenu.visibility = View.VISIBLE
    }

    fun hideSettings() {
        layoutSettingsMenu.visibility = View.GONE
    }

    fun showLineSelection() {
        layoutLineMenu.visibility = View.VISIBLE
    }

    fun hideLineSelection() {
        layoutLineMenu.visibility = View.GONE
    }

    fun extendOsdTimeout() {
        if (isOsdVisible()) {
            uiHandler.removeCallbacks(hideOsdRunnable)
            uiHandler.postDelayed(hideOsdRunnable, 5000)
        }
    }

    fun onDestroy() {
        uiHandler.removeCallbacks(hideOsdRunnable)
    }
}
