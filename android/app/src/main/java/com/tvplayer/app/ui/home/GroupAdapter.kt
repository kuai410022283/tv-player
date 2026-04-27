package com.tvplayer.app.ui.home

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.tvplayer.app.R
import com.tvplayer.app.data.model.ChannelGroup

/**
 * 分组列表适配器 - TV模式使用
 */
class GroupAdapter(
    private val onClick: (ChannelGroup) -> Unit
) : ListAdapter<ChannelGroup, GroupAdapter.ViewHolder>(DiffCallback()) {

    private var selectedId = 0L

    fun setSelected(id: Long) {
        val old = selectedId
        selectedId = id
        currentList.forEachIndexed { index, group ->
            if (group.id == old || group.id == id) {
                notifyItemChanged(index)
            }
        }
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): ViewHolder {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_group, parent, false)
        return ViewHolder(view)
    }

    override fun onBindViewHolder(holder: ViewHolder, position: Int) {
        val item = getItem(position)
        holder.bind(item, item.id == selectedId)
        holder.itemView.setOnClickListener { onClick(item) }

        // TV 焦点动画
        holder.itemView.setOnFocusChangeListener { v, hasFocus ->
            v.animate()
                .alpha(if (hasFocus) 1.0f else if (item.id == selectedId) 1.0f else 0.7f)
                .scaleX(if (hasFocus) 1.05f else 1.0f)
                .scaleY(if (hasFocus) 1.05f else 1.0f)
                .setDuration(120)
                .start()
        }
    }

    class ViewHolder(itemView: View) : RecyclerView.ViewHolder(itemView) {
        private val tvName: TextView = itemView.findViewById(R.id.tvGroupName)
        private val indicator: View = itemView.findViewById(R.id.viewIndicator)

        fun bind(item: ChannelGroup, selected: Boolean) {
            tvName.text = item.name
            tvName.isSelected = selected
            indicator.visibility = if (selected) View.VISIBLE else View.INVISIBLE
            itemView.alpha = if (selected) 1.0f else 0.7f
        }
    }

    class DiffCallback : DiffUtil.ItemCallback<ChannelGroup>() {
        override fun areItemsTheSame(a: ChannelGroup, b: ChannelGroup) = a.id == b.id
        override fun areContentsTheSame(a: ChannelGroup, b: ChannelGroup) = a == b
    }
}
