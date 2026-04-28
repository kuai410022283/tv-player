package com.tvplayer.app.util

import android.view.FocusFinder
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

/**
 * TV 遥控器焦点导航辅助器
 * 解决 RecyclerView 焦点丢失、焦点跳跃等常见问题
 */
object FocusHelper {

    /**
     * 为 RecyclerView 设置 TV 优化的焦点行为
     */
    fun setupTvRecyclerView(rv: RecyclerView) {
        rv.apply {
            // 确保获得焦点
            isFocusable = true
            descendantFocusability = ViewGroup.FOCUS_AFTER_DESCENDANTS

            // 焦点变化时滚动到可见
            addOnChildAttachStateChangeListener(object : RecyclerView.OnChildAttachStateChangeListener {
                override fun onChildViewAttachedToWindow(view: View) {
                    view.setOnFocusChangeListener { v, hasFocus ->
                        if (hasFocus) {
                            val pos = rv.getChildAdapterPosition(v)
                            if (pos != RecyclerView.NO_POSITION) {
                                smoothScrollToPosition(pos)
                            }
                            // 焦点态动画
                            v.animate()
                                .scaleX(if (hasFocus) 1.03f else 1.0f)
                                .scaleY(if (hasFocus) 1.03f else 1.0f)
                                .alpha(if (hasFocus) 1.0f else 0.85f)
                                .setDuration(150)
                                .start()
                        }
                    }
                }
                override fun onChildViewDetachedFromWindow(view: View) {}
            })
        }
    }

    /**
     * 在两个 RecyclerView 之间建立左右焦点导航
     * 左列表按右键 → 右列表获焦
     * 右列表按左键 → 左列表获焦
     */
    fun linkHorizontalFocus(leftRv: RecyclerView, rightRv: RecyclerView) {
        leftRv.setOnKeyListener { _, keyCode, event ->
            if (keyCode == KeyEvent.KEYCODE_DPAD_RIGHT && event.action == KeyEvent.ACTION_DOWN) {
                rightRv.requestFocus()
                // 让右列表聚焦到第一个可见项
                val firstVisible = (rightRv.layoutManager as? LinearLayoutManager)
                    ?.findFirstVisibleItemPosition() ?: 0
                rightRv.getChildAt(firstVisible - (rightRv.layoutManager as LinearLayoutManager).findFirstVisibleItemPosition())
                    ?.requestFocus()
                true
            } else false
        }

        rightRv.setOnKeyListener { _, keyCode, event ->
            if (keyCode == KeyEvent.KEYCODE_DPAD_LEFT && event.action == KeyEvent.ACTION_DOWN) {
                leftRv.requestFocus()
                true
            } else false
        }
    }

    /**
     * 设置 RecyclerView 的上下边界行为
     * 到达列表顶部/底部时，焦点传递给父容器
     */
    fun setupBoundaryFocus(rv: RecyclerView, onTopReach: (() -> Unit)? = null, onBottomReach: (() -> Unit)? = null) {
        rv.setOnKeyListener { _, keyCode, event ->
            if (event.action != KeyEvent.ACTION_DOWN) return@setOnKeyListener false

            val layoutManager = rv.layoutManager as? LinearLayoutManager ?: return@setOnKeyListener false
            val firstVisible = layoutManager.findFirstCompletelyVisibleItemPosition()
            val lastVisible = layoutManager.findLastCompletelyVisibleItemPosition()
            val itemCount = rv.adapter?.itemCount ?: 0

            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> {
                    if (firstVisible == 0) {
                        // 检查当前焦点是否在第一个item上
                        val focusedChild = rv.findFocus()
                        if (focusedChild != null && rv.getChildAdapterPosition(focusedChild) == 0) {
                            onTopReach?.invoke()
                            return@setOnKeyListener false // 让焦点自然传递
                        }
                    }
                    false
                }
                KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (lastVisible >= itemCount - 1) {
                        val focusedChild = rv.findFocus()
                        val lastChild = layoutManager.findViewByPosition(itemCount - 1)
                        if (focusedChild == lastChild) {
                            onBottomReach?.invoke()
                            return@setOnKeyListener false
                        }
                    }
                    false
                }
                else -> false
            }
        }
    }

    /**
     * 请求初始焦点
     */
    fun requestInitialFocus(vararg views: View) {
        views.firstOrNull()?.post { it.requestFocus() }
    }
}
