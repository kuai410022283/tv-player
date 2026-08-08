package com.mediaplayer.app.ui.home

import android.graphics.Color
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import com.mediaplayer.app.R
import com.mediaplayer.app.data.model.EPGProgram
import java.text.SimpleDateFormat
import java.util.*

class EpgAdapter : RecyclerView.Adapter<EpgAdapter.ViewHolder>() {

    private val programs = mutableListOf<EPGProgram>()
    private var playingIndex = -1

    private var supportCatchup = false
    private var itemClickListener: ((EPGProgram) -> Unit)? = null
    private var activeProgramStartTime: String? = null

    fun setSupportCatchup(support: Boolean) {
        this.supportCatchup = support
    }
    
    fun setActiveProgramStartTime(startTime: String?) {
        this.activeProgramStartTime = startTime
    }

    fun setOnItemClickListener(listener: (EPGProgram) -> Unit) {
        this.itemClickListener = listener
    }

    fun setData(newPrograms: List<EPGProgram>) {
        programs.clear()
        programs.addAll(newPrograms)
        
        // 查找当前应该高亮的节目 (优先判断是否有主动选中的回看节目)
        playingIndex = -1
        if (activeProgramStartTime != null) {
            playingIndex = programs.indexOfFirst { it.startTime == activeProgramStartTime }
        }
        
        // 如果没有选中的回看节目，或者没找到，则使用当前时间判断直播节目
        val now = Date()
        val liveIndex = programs.indexOfFirst {
            try {
                val start = parseIsoTime(it.startTime)
                val end = parseIsoTime(it.endTime)
                start != null && end != null && now.after(start) && now.before(end)
            } catch (e: Exception) { false }
        }
        
        if (playingIndex == -1) {
            playingIndex = liveIndex
        }

        notifyDataSetChanged()
    }
    
    private fun parseIsoTime(timeStr: String): Date? {
        if (timeStr.isEmpty()) return null
        val patterns = arrayOf(
            "yyyy-MM-dd'T'HH:mm:ss.SSSXXX",
            "yyyy-MM-dd'T'HH:mm:ssXXX",
            "yyyy-MM-dd'T'HH:mm:ss.SSS",
            "yyyy-MM-dd'T'HH:mm:ss"
        )
        for (pattern in patterns) {
            try {
                val sdf = SimpleDateFormat(pattern, Locale.getDefault())
                val date = sdf.parse(timeStr)
                if (date != null) return date
            } catch (e: Exception) {
                // Try next
            }
        }
        try {
            var normalized = timeStr
            if (normalized.endsWith("Z")) {
                normalized = normalized.substring(0, normalized.length - 1) + "+0000"
            } else {
                val len = normalized.length
                if (len > 6 && (normalized[len - 3] == ':' && (normalized[len - 6] == '+' || normalized[len - 6] == '-'))) {
                    normalized = normalized.substring(0, len - 3) + normalized.substring(len - 2)
                }
            }
            val sdf = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ssZ", Locale.getDefault())
            return sdf.parse(normalized)
        } catch (e: Exception) {
            try {
                val sdf = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
                return sdf.parse(timeStr)
            } catch (ex: Exception) {
                return null
            }
        }
    }
    
    fun getPlayingIndex(): Int = playingIndex

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_epg, parent, false)
        return ViewHolder(view)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val prog = programs[position]
        
        // format time from "2026-05-25T19:00:00Z" to "19:00"
        var timeStr = ""
        try {
            val date = parseIsoTime(prog.startTime)
            if (date != null) {
                val sdfOut = SimpleDateFormat("HH:mm", Locale.getDefault())
                timeStr = sdfOut.format(date)
            }
        } catch (e: Exception) {
            timeStr = prog.startTime
        }
        
        holder.tvTime.text = timeStr
        holder.tvTitle.text = prog.title
        
        val isLiveProgram = position == programs.indexOfFirst {
            val now = Date()
            try {
                val start = parseIsoTime(it.startTime)
                val end = parseIsoTime(it.endTime)
                start != null && end != null && now.after(start) && now.before(end)
            } catch (e: Exception) { false }
        }
        
        if (position == playingIndex) {
            holder.tvTime.setTextColor(Color.parseColor("#FFC107")) // accent color
            holder.tvTitle.setTextColor(Color.parseColor("#FFC107"))
            if (activeProgramStartTime != null) {
                holder.tvTitle.text = "${prog.title} (" + holder.itemView.context.getString(R.string.epg_status_playback) + ")"
            } else {
                holder.tvTitle.text = "${prog.title} (" + holder.itemView.context.getString(R.string.epg_status_now_playing) + ")"
            }
            holder.ivCatchup.visibility = View.GONE
        } else if (position < (if (activeProgramStartTime != null) programs.size else playingIndex) && supportCatchup && !isLiveProgram) {
            // 这里判断是否是往期节目。如果是回看模式，且位置在当前直播节目前面，就显示回看图标
            val liveIdx = programs.indexOfFirst {
                val now = Date()
                try {
                    val start = parseIsoTime(it.startTime)
                    val end = parseIsoTime(it.endTime)
                    start != null && end != null && now.after(start) && now.before(end)
                } catch (e: Exception) { false }
            }
            
            if (position < liveIdx || (liveIdx == -1 && position < programs.size)) {
                holder.tvTime.setTextColor(Color.WHITE)
                holder.tvTitle.setTextColor(Color.parseColor("#DDDDDD"))
                holder.tvTitle.text = prog.title
                holder.ivCatchup.visibility = View.VISIBLE
            } else {
                holder.tvTime.setTextColor(Color.WHITE)
                holder.tvTitle.setTextColor(Color.parseColor("#DDDDDD"))
                holder.tvTitle.text = prog.title
                holder.ivCatchup.visibility = View.GONE
            }
        } else {
            holder.tvTime.setTextColor(Color.WHITE)
            holder.tvTitle.setTextColor(Color.parseColor("#DDDDDD"))
            holder.tvTitle.text = prog.title
            holder.ivCatchup.visibility = View.GONE
        }
        
        // 点击事件
        holder.itemView.setOnClickListener {
            val liveIdx = programs.indexOfFirst {
                val now = Date()
                try {
                    val start = parseIsoTime(it.startTime)
                    val end = parseIsoTime(it.endTime)
                    start != null && end != null && now.after(start) && now.before(end)
                } catch (e: Exception) { false }
            }
            if ((position < liveIdx || (liveIdx == -1 && position < programs.size)) && supportCatchup) {
                itemClickListener?.invoke(prog)
            }
        }
        
        // 初始化当前状态
        holder.tvTitle.isSelected = holder.itemView.hasFocus()
        
        // 关键：当条目获取焦点时，设置 isSelected=true 以激活跑马灯 (Marquee) 效果
        holder.itemView.setOnFocusChangeListener { _, hasFocus ->
            holder.tvTitle.isSelected = hasFocus
        }
    }

    override fun getItemCount() = programs.size

    class ViewHolder(view: View) : RecyclerView.ViewHolder(view) {
        val tvTime: TextView = view.findViewById(R.id.tvEpgTime)
        val tvTitle: TextView = view.findViewById(R.id.tvEpgTitle)
        val ivCatchup: android.widget.ImageView = view.findViewById(R.id.ivCatchup)
    }
}
