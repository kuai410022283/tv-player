package com.tvplayer.app.ui.home

import android.graphics.Color
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.RecyclerView
import com.tvplayer.app.R
import com.tvplayer.app.data.model.EPGProgram
import java.text.SimpleDateFormat
import java.util.*

class EpgAdapter : RecyclerView.Adapter<EpgAdapter.ViewHolder>() {

    private val programs = mutableListOf<EPGProgram>()
    private var playingIndex = -1

    fun setData(newPrograms: List<EPGProgram>) {
        programs.clear()
        programs.addAll(newPrograms)
        
        // 查找当前正在播放的节目
        playingIndex = -1
        val now = Date()
        val sdf = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
        try {
            for (i in programs.indices) {
                val start = sdf.parse(programs[i].startTime)
                val end = sdf.parse(programs[i].endTime)
                if (start != null && end != null && now.after(start) && now.before(end)) {
                    playingIndex = i
                    break
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
        }
        
        notifyDataSetChanged()
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
            val sdfIn = SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss", Locale.getDefault())
            val date = sdfIn.parse(prog.startTime)
            if (date != null) {
                val sdfOut = SimpleDateFormat("HH:mm", Locale.getDefault())
                timeStr = sdfOut.format(date)
            }
        } catch (e: Exception) {
            timeStr = prog.startTime
        }
        
        holder.tvTime.text = timeStr
        holder.tvTitle.text = prog.title
        
        if (position == playingIndex) {
            holder.tvTime.setTextColor(Color.parseColor("#FFC107")) // accent color
            holder.tvTitle.setTextColor(Color.parseColor("#FFC107"))
            holder.tvTitle.text = "${prog.title} (正在播出)"
        } else {
            holder.tvTime.setTextColor(Color.WHITE)
            holder.tvTitle.setTextColor(Color.parseColor("#DDDDDD"))
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
    }
}
