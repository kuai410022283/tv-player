package com.mediaplayer.app.ui.widget

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.AttributeSet
import android.view.LayoutInflater
import android.view.View
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import com.mediaplayer.app.R
import com.mediaplayer.app.util.RemoteLogger

class OsdOverlayView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyleAttr: Int = 0
) : FrameLayout(context, attrs, defStyleAttr) {

    private val layoutOsd: LinearLayout
    private val tvOsdChannelNum: TextView
    private val tvOsdChannelName: TextView
    private val tvOsdLineInfo: TextView
    private val tvOsdInfo: TextView
    private val tvOsdEpg: TextView
    private val tvOsdNextEpg: TextView
    private val progressEpg: ProgressBar

    private val uiHandler = Handler(Looper.getMainLooper())
    private val hideOsdRunnable = Runnable {
        hideOsd()
    }

    var onOsdVisibilityChanged: ((Boolean) -> Unit)? = null

    init {
        LayoutInflater.from(context).inflate(R.layout.view_osd_overlay, this, true)

        layoutOsd = findViewById(R.id.layoutOsd)
        tvOsdChannelNum = findViewById(R.id.tvOsdChannelNum)
        tvOsdChannelName = findViewById(R.id.tvOsdChannelName)
        tvOsdLineInfo = findViewById(R.id.tvOsdLineInfo)
        tvOsdInfo = findViewById(R.id.tvOsdInfo)
        tvOsdEpg = findViewById(R.id.tvOsdEpg)
        tvOsdNextEpg = findViewById(R.id.tvOsdNextEpg)
        progressEpg = findViewById(R.id.progressEpg)
    }

    override fun onDetachedFromWindow() {
        super.onDetachedFromWindow()
        uiHandler.removeCallbacks(hideOsdRunnable)
    }

    fun showOsd() {
        layoutOsd.visibility = View.VISIBLE
        
        // Ensure marquee works by selecting the views programmatically
        tvOsdChannelName.isSelected = true
        tvOsdEpg.isSelected = true
        tvOsdNextEpg.isSelected = true

        uiHandler.removeCallbacks(hideOsdRunnable)
        uiHandler.postDelayed(hideOsdRunnable, 5000)
        
        onOsdVisibilityChanged?.invoke(true)
    }

    fun hideOsd() {
        layoutOsd.visibility = View.GONE
        RemoteLogger.i("PanelTrace", "OSD GONE")
        onOsdVisibilityChanged?.invoke(false)
    }

    fun isOsdVisible(): Boolean {
        return layoutOsd.visibility == View.VISIBLE
    }
    
    fun removeCallbacks() {
        uiHandler.removeCallbacks(hideOsdRunnable)
    }

    // Setters for MainActivity to update text
    fun setChannelNum(numStr: String) {
        tvOsdChannelNum.text = numStr
    }

    fun setChannelName(nameStr: String) {
        tvOsdChannelName.text = nameStr
        tvOsdChannelName.isSelected = true
    }

    fun setLineInfo(lineStr: String) {
        tvOsdLineInfo.text = lineStr
    }

    fun setInfoText(infoStr: String) {
        tvOsdInfo.text = infoStr
    }

    fun setEpgText(epgStr: String) {
        tvOsdEpg.text = epgStr
        tvOsdEpg.isSelected = true
    }

    fun setNextEpgText(nextEpgStr: String) {
        if (nextEpgStr.isNotEmpty()) {
            tvOsdNextEpg.text = nextEpgStr
            tvOsdNextEpg.visibility = View.VISIBLE
            tvOsdNextEpg.isSelected = true
        } else {
            tvOsdNextEpg.text = ""
            tvOsdNextEpg.visibility = View.GONE
        }
    }

    fun setEpgProgress(progress: Int) {
        if (progress in 1..100) {
            progressEpg.progress = progress
            progressEpg.visibility = View.VISIBLE
        } else {
            progressEpg.progress = 0
            progressEpg.visibility = View.GONE
        }
    }

    fun getInfoText(): String {
        return tvOsdInfo.text.toString()
    }
}
