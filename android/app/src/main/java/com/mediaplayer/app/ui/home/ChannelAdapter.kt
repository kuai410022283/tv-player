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
    private val onLongClick: ((Channel, Int) -> Unit)? = null,
    private val onFocus: ((Channel, Int) -> Unit)? = null
) : RecyclerView.Adapter<ChannelAdapter.ViewHolder>() {

    var showLogo: Boolean = true
    private var channels: List<Channel> = emptyList()
    private var playingChannelId: Long = -1L

    fun setData(list: List<Channel>) {
        val oldList = this.channels
        val newList = list
        
        // 如果数据量过大，DiffUtil 在主线程计算会造成明显卡顿
        // 分组切换通常是全量数据替换，直接使用 notifyDataSetChanged 性能最好
        if (oldList.size > 200 || newList.size > 200) {
            this.channels = newList
            notifyDataSetChanged()
            return
        }

        val diffResult = DiffUtil.calculateDiff(object : DiffUtil.Callback() {
            override fun getOldListSize() = oldList.size
            override fun getNewListSize() = newList.size
            override fun areItemsTheSame(oldItemPosition: Int, newItemPosition: Int): Boolean {
                return oldList[oldItemPosition].id == newList[newItemPosition].id
            }
            override fun areContentsTheSame(oldItemPosition: Int, newItemPosition: Int): Boolean {
                val o = oldList[oldItemPosition]
                val n = newList[newItemPosition]
                return o.name == n.name && o.logo == n.logo && o.currentEpg == n.currentEpg
            }
        })
        this.channels = newList
        diffResult.dispatchUpdatesTo(this)
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
        val layoutId = R.layout.item_channel
        val view = LayoutInflater.from(parent.context).inflate(layoutId, parent, false)
        return ViewHolder(view)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int, payloads: MutableList<Any>) {
        if (payloads.isNotEmpty()) {
            for (payload in payloads) {
                if (payload == "epg_update") {
                    val item = getItem(position)
                    holder.bindEpgOnly(item)
                    return
                } else if (payload == "fav_update") {
                    val item = getItem(position)
                    holder.bindFavOnly(item)
                    return
                }
            }
        }
        super.onBindViewHolder(holder, position, payloads)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val item = getItem(position)
        val isPlaying = item.id == playingChannelId
        
        holder.itemView.findViewById<TextView>(R.id.tvChannelName)?.setTextColor(
            if (isPlaying) android.graphics.Color.parseColor("#00E5FF") 
            else android.graphics.Color.parseColor("#E0E0E0")
        )
        
        holder.bind(item, isPlaying, showLogo)

        // 点击事件
        holder.itemView.setOnClickListener { onClick(item, position) }
        
        // 长按事件
        holder.itemView.setOnLongClickListener { 
            onLongClick?.invoke(item, position)
            true
        }

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

        fun bind(item: Channel, isPlaying: Boolean, showLogo: Boolean = true) {
            tvIndex.text = String.format("%03d", item.globalIndex + 1)
            tvName.text = item.name
            
            // 下方显示当前 EPG，如果没有则占位
            if (item.currentEpg.isNotEmpty()) {
                tvCurrentEpg.text = item.currentEpg
                tvCurrentEpg.visibility = View.VISIBLE
                val dynamicPercent = item.getDynamicEpgPercent()
                if (dynamicPercent > 0) {
                    progressEpgItem.progress = dynamicPercent
                    progressEpgItem.visibility = View.VISIBLE
                } else {
                    progressEpgItem.visibility = View.GONE
                }
            } else {
                tvCurrentEpg.text = "暂无节目信息"
                tvCurrentEpg.visibility = View.VISIBLE
                progressEpgItem.visibility = View.GONE
            }

            // 右侧流类型角标
            val lines = item.getLinesSafely()
            val streamType = if (lines.isNotEmpty()) lines[0].streamType else item.legacyStreamType
            tvTypeBadge.text = if (streamType.isEmpty()) "AUTO" else streamType.uppercase()
            tvTypeBadge.visibility = View.VISIBLE

            if (showLogo) {
                ivLogo.visibility = View.VISIBLE
                if (item.logo.isNotEmpty()) {
                    var loadUrl = item.logo
                    if (loadUrl.startsWith("/")) {
                        val serverUrl = com.mediaplayer.app.data.api.ApiClient.getServerUrl().trimEnd('/')
                        loadUrl = serverUrl + loadUrl
                    }
                    ivLogo.load(loadUrl) {
                        placeholder(R.drawable.ic_channel_placeholder)
                        error(R.drawable.ic_channel_placeholder)
                    }
                } else {
                    ivLogo.load(R.drawable.ic_channel_placeholder)
                }
            } else {
                ivLogo.visibility = View.GONE
            }

            val isFav = com.mediaplayer.app.data.FavoriteManager.isFavorite(item.id)
            ivFav.visibility = View.GONE
            if (isFav) {
                tvIndex.setTextColor(android.graphics.Color.parseColor("#FFD700"))
            } else {
                tvIndex.setTextColor(androidx.core.content.ContextCompat.getColor(itemView.context, R.color.text_secondary))
            }

            playingIndicator.visibility = if (isPlaying) View.VISIBLE else View.GONE
            itemView.isActivated = isPlaying
        }

        fun bindEpgOnly(item: Channel) {
            if (item.currentEpg.isNotEmpty()) {
                tvCurrentEpg.text = item.currentEpg
                tvCurrentEpg.visibility = View.VISIBLE
                val dynamicPercent = item.getDynamicEpgPercent()
                if (dynamicPercent > 0) {
                    progressEpgItem.progress = dynamicPercent
                    progressEpgItem.visibility = View.VISIBLE
                } else {
                    progressEpgItem.visibility = View.GONE
                }
            } else {
                tvCurrentEpg.text = "暂无节目信息"
                tvCurrentEpg.visibility = View.VISIBLE
                progressEpgItem.visibility = View.GONE
            }
        }
        
        fun bindFavOnly(item: Channel) {
            val isFav = com.mediaplayer.app.data.FavoriteManager.isFavorite(item.id)
            if (isFav) {
                tvIndex.setTextColor(android.graphics.Color.parseColor("#FFD700"))
            } else {
                tvIndex.setTextColor(androidx.core.content.ContextCompat.getColor(itemView.context, R.color.text_secondary))
            }
        }
    }
}
