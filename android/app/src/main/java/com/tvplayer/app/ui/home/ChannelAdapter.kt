package com.tvplayer.app.ui.home

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import coil.load
import com.tvplayer.app.R
import com.tvplayer.app.data.model.Channel

/**
 * 频道列表适配器 - 同时支持 TV (D-pad焦点) 和 手机 (触控)
 */
class ChannelAdapter(
    private val isTvMode: Boolean = true,
    private val onClick: (Channel, Int) -> Unit
) : ListAdapter<Channel, ChannelAdapter.ViewHolder>(DiffCallback()) {

    private var playingIndex = -1

    fun setPlayingIndex(index: Int) {
        val old = playingIndex
        playingIndex = index
        if (old >= 0) notifyItemChanged(old)
        if (index >= 0) notifyItemChanged(index)
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
        val layoutId = if (isTvMode) R.layout.item_channel else R.layout.item_channel_phone
        val view = LayoutInflater.from(parent.context).inflate(layoutId, parent, false)
        return ViewHolder(view)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val item = getItem(position)
        holder.bind(item, position == playingIndex)

        // 点击事件
        holder.itemView.setOnClickListener { onClick(item, position) }

        if (isTvMode) {
            // TV 模式: D-pad 焦点处理
            holder.itemView.isFocusable = true
            holder.itemView.setOnFocusChangeListener { v, hasFocus ->
                v.animate()
                    .scaleX(if (hasFocus) 1.03f else 1.0f)
                    .scaleY(if (hasFocus) 1.03f else 1.0f)
                    .alpha(if (hasFocus) 1.0f else 0.85f)
                    .setDuration(120)
                    .start()
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
        private val tvType: TextView = itemView.findViewById(R.id.tvStreamType)
        private val ivLogo: ImageView = itemView.findViewById(R.id.ivChannelLogo)
        private val ivFav: ImageView = itemView.findViewById(R.id.ivFavorite)
        private val ivStatus: ImageView = itemView.findViewById(R.id.ivStatus)
        private val playingIndicator: View = itemView.findViewById(R.id.viewPlaying)

        fun bind(item: Channel, isPlaying: Boolean) {
            tvIndex.text = String.format("%02d", adapterPosition + 1)
            tvName.text = item.name
            tvType.text = item.streamType.uppercase()

            if (item.logo.isNotEmpty()) {
                ivLogo.load(item.logo) {
                    placeholder(R.drawable.ic_channel_placeholder)
                    error(R.drawable.ic_channel_placeholder)
                }
                ivLogo.visibility = View.VISIBLE
            } else {
                ivLogo.visibility = View.GONE
            }

            ivFav.visibility = if (item.isFavorite) View.VISIBLE else View.GONE

            when (item.status) {
                "online" -> {
                    ivStatus.setImageResource(R.drawable.ic_status_online)
                    ivStatus.visibility = View.VISIBLE
                }
                "offline" -> {
                    ivStatus.setImageResource(R.drawable.ic_status_offline)
                    ivStatus.visibility = View.VISIBLE
                }
                else -> ivStatus.visibility = View.GONE
            }

            playingIndicator.visibility = if (isPlaying) View.VISIBLE else View.GONE
        }
    }

    class DiffCallback : DiffUtil.ItemCallback<Channel>() {
        override fun areItemsTheSame(a: Channel, b: Channel) = a.id == b.id
        override fun areContentsTheSame(a: Channel, b: Channel) = a == b
    }
}
