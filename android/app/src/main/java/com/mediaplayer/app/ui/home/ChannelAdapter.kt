package com.mediaplayer.app.ui.home

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import coil.load
import com.mediaplayer.app.R
import com.mediaplayer.app.data.model.Channel

/**
 * 频道列表适配器 - 同时支持 TV (D-pad焦点) 和 手机 (触控)
 */
class ChannelAdapter(
    private val isTvMode: Boolean = false,
    private val onClick: (Channel, Int) -> Unit,
    private val onFocus: ((Channel, Int) -> Unit)? = null
) : RecyclerView.Adapter<ChannelAdapter.ViewHolder>() {

    private var channels: List<Channel> = emptyList()
    private var playingChannelId: Long = -1L

    fun setData(list: List<Channel>) {
        this.channels = list
        notifyDataSetChanged()
    }

    override fun getItemCount(): Int = channels.size
    
    fun getItem(position: Int): Channel = channels[position]

    fun setPlayingChannelId(id: Long) {
        val oldId = playingChannelId
        playingChannelId = id
        
        val oldIndex = channels.indexOfFirst { it.id == oldId }
        val newIndex = channels.indexOfFirst { it.id == id }
        
        if (oldIndex >= 0) notifyItemChanged(oldIndex, "play_state_changed")
        if (newIndex >= 0) notifyItemChanged(newIndex, "play_state_changed")
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
        val layoutId = if (isTvMode) R.layout.item_channel else R.layout.item_channel_phone
        val view = LayoutInflater.from(parent.context).inflate(layoutId, parent, false)
        return ViewHolder(view)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val item = getItem(position)
        val isPlaying = item.id == playingChannelId
        
        holder.itemView.findViewById<TextView>(R.id.tvChannelName)?.setTextColor(
            if (isPlaying) android.graphics.Color.parseColor("#00E5FF") 
            else android.graphics.Color.parseColor("#E0E0E0")
        )
        
        holder.bind(item, isPlaying)

        // 点击事件
        holder.itemView.setOnClickListener { onClick(item, position) }

        if (isTvMode) {
            // TV 模式: D-pad 焦点处理，并且防止触控模式下的双击问题
            holder.itemView.isFocusable = true
            holder.itemView.isFocusableInTouchMode = false
            holder.itemView.findViewById<TextView>(R.id.tvCurrentEpg)?.isSelected = holder.itemView.hasFocus()
            holder.itemView.findViewById<TextView>(R.id.tvChannelName)?.isSelected = holder.itemView.hasFocus()
            holder.itemView.setOnFocusChangeListener { v, hasFocus ->
                v.animate()
                    .scaleX(if (hasFocus) 1.03f else 1.0f)
                    .scaleY(if (hasFocus) 1.03f else 1.0f)
                    .alpha(if (hasFocus) 1.0f else 0.85f)
                    .setDuration(120)
                    .start()
                
                v.findViewById<TextView>(R.id.tvCurrentEpg)?.isSelected = hasFocus
                v.findViewById<TextView>(R.id.tvChannelName)?.isSelected = hasFocus
            }
        } else {
            // 手机模式: 触控反馈
            holder.itemView.isFocusable = false
            holder.itemView.setOnTouchListener { v, event ->
                when (event.action) {
                    android.view.MotionEvent.ACTION_DOWN -> {
                        v.animate().alpha(0.7f).setDuration(80).start()
                    }
                    android.view.MotionEvent.ACTION_UP,
                    android.view.MotionEvent.ACTION_CANCEL -> {
                        v.animate().alpha(1.0f).setDuration(80).start()
                    }
                }
                false
            }
        }
    }

    class ViewHolder(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val tvIndex: TextView = itemView.findViewById(R.id.tvChannelIndex)
        private val tvName: TextView = itemView.findViewById(R.id.tvChannelName)
        private val tvCurrentEpg: TextView = itemView.findViewById(R.id.tvCurrentEpg)
        private val tvTypeBadge: TextView = itemView.findViewById(R.id.tvStreamTypeBadge)
        private val progressEpgItem: android.widget.ProgressBar = itemView.findViewById(R.id.progressEpgItem)
        private val ivLogo: ImageView = itemView.findViewById(R.id.ivChannelLogo)
        private val ivFav: ImageView = itemView.findViewById(R.id.ivFavorite)
        private val playingIndicator: View = itemView.findViewById(R.id.viewPlaying)

        fun bind(item: Channel, isPlaying: Boolean) {
            tvIndex.text = String.format("%03d", item.globalIndex + 1)
            tvName.text = item.name
            
            // 下方显示当前 EPG，如果没有则占位
            if (item.currentEpg.isNotEmpty()) {
                tvCurrentEpg.text = item.currentEpg
                tvCurrentEpg.visibility = View.VISIBLE
                if (item.epgPercent > 0) {
                    progressEpgItem.progress = item.epgPercent
                    progressEpgItem.visibility = View.VISIBLE
                } else {
                    progressEpgItem.visibility = View.GONE
                }
            } else {
                tvCurrentEpg.text = "精彩节目"
                tvCurrentEpg.visibility = View.VISIBLE
                progressEpgItem.visibility = View.GONE
            }

            // 右侧流类型角标
            val lines = item.getLinesSafely()
            val streamType = if (lines.isNotEmpty()) lines[0].streamType else item.legacyStreamType
            tvTypeBadge.text = streamType.uppercase()
            tvTypeBadge.visibility = View.VISIBLE

            if (item.logo.isNotEmpty()) {
                ivLogo.load(item.logo) {
                    placeholder(R.drawable.ic_channel_placeholder)
                    error(R.drawable.ic_channel_placeholder)
                }
                ivLogo.visibility = View.VISIBLE
            } else {
                ivLogo.visibility = View.GONE
            }

            ivFav.visibility = View.GONE

            playingIndicator.visibility = if (isPlaying) View.VISIBLE else View.GONE
            itemView.isActivated = isPlaying
        }
    }
}
