package com.tvplayer.app.util

import android.content.Context
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.View
import kotlin.math.abs

/**
 * 播放器手势控制器
 * - 左右滑动：切换频道
 * - 上下滑动：调节音量/亮度
 * - 单击：显示/隐藏控制层
 * - 双击：播放/暂停
 * - 长按：倍速播放
 */
class PlayerGestureController(
    private val context: Context,
    private val listener: GestureListener
) {

    interface GestureListener {
        fun onChannelNext()
        fun onChannelPrev()
        fun onToggleInfo()
        fun onTogglePlayPause()
        fun onVolumeChange(delta: Float)   // -1.0 ~ +1.0
        fun onBrightnessChange(delta: Float) // -1.0 ~ +1.0
        fun onSeekDelta(deltaMs: Long)
        fun onLongPressStart()
        fun onLongPressEnd()
    }

    private var isLongPressing = false

    private val gestureDetector = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {

        override fun onSingleTapConfirmed(e: MotionEvent): Boolean {
            listener.onToggleInfo()
            return true
        }

        override fun onDoubleTap(e: MotionEvent): Boolean {
            listener.onTogglePlayPause()
            return true
        }

        override fun onLongPress(e: MotionEvent) {
            isLongPressing = true
            listener.onLongPressStart()
        }

        override fun onScroll(e1: MotionEvent?, e2: MotionEvent, distanceX: Float, distanceY: Float): Boolean {
            if (e1 == null) return false

            val dx = e2.x - e1.x
            val dy = e2.y - e1.y
            val absDx = abs(dx)
            val absDy = abs(dy)

            // 确定主方向
            if (absDx > absDy && absDx > 50) {
                // 水平滑动 → 切换频道
                if (dx > 0) listener.onChannelPrev() else listener.onChannelNext()
                return true
            } else if (absDy > absDx && absDy > 30) {
                // 垂直滑动
                val screenWidth = context.resources.displayMetrics.widthPixels
                if (e1.x < screenWidth / 2) {
                    // 左半屏 → 亮度
                    listener.onBrightnessChange(-dy / 300f)
                } else {
                    // 右半屏 → 音量
                    listener.onVolumeChange(-dy / 300f)
                }
                return true
            }

            return false
        }

        override fun onFling(e1: MotionEvent?, e2: MotionEvent, velocityX: Float, velocityY: Float): Boolean {
            if (e1 == null) return false
            val dx = e2.x - e1.x
            val dy = e2.y - e1.y

            if (abs(dx) > abs(dy) && abs(dx) > 100) {
                if (dx > 0) listener.onChannelPrev() else listener.onChannelNext()
                return true
            }
            return false
        }
    })

    fun onTouchEvent(event: MotionEvent): Boolean {
        when (event.action) {
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                if (isLongPressing) {
                    isLongPressing = false
                    listener.onLongPressEnd()
                }
            }
        }
        return gestureDetector.onTouchEvent(event)
    }

    /**
     * 绑定到 View
     */
    fun attachTo(view: View) {
        view.setOnTouchListener { _, event -> onTouchEvent(event) }
    }
}
