package com.mediaplayer.app.ui.home.delegates

import android.app.Activity
import android.content.Context
import android.media.AudioManager
import android.provider.Settings
import android.view.GestureDetector
import android.view.MotionEvent
import android.view.View

class TouchGestureDelegate(
    private val activity: Activity,
    private val videoLayout: View,
    private val callbacks: Callbacks
) {
    interface Callbacks {
        fun onSingleTap()
        fun onLongPress()
        fun onDoubleTap()
        fun onSwipeUp() // Next channel
        fun onSwipeDown() // Previous channel
        fun onSwipeLeft()
        fun onSwipeRight()
    }

    private var initialBrightness = -1f
    private var initialVolume = 0
    private var isAdjusting = false
    private var adjustMode = 0 // 0: none, 1: brightness, 2: volume

    fun attach() {
        val gestureDetector = GestureDetector(activity, object : GestureDetector.SimpleOnGestureListener() {
            override fun onDown(e: MotionEvent): Boolean {
                isAdjusting = false
                adjustMode = 0
                val screenWidth = videoLayout.width
                if (screenWidth > 0) {
                    if (e.x > screenWidth * 0.05f && e.x < screenWidth * 0.15f) {
                        adjustMode = 1
                        val lp = activity.window.attributes
                        initialBrightness = lp.screenBrightness
                        if (initialBrightness < 0) {
                            try {
                                val sysBrightness = Settings.System.getInt(activity.contentResolver, Settings.System.SCREEN_BRIGHTNESS)
                                initialBrightness = sysBrightness / 255f
                            } catch (e: Exception) {
                                initialBrightness = 0.5f
                            }
                        }
                    } else if (e.x < screenWidth * 0.95f && e.x > screenWidth * 0.85f) {
                        adjustMode = 2
                        val audioManager = activity.getSystemService(Context.AUDIO_SERVICE) as AudioManager
                        initialVolume = audioManager.getStreamVolume(AudioManager.STREAM_MUSIC)
                    }
                }
                return true
            }

            override fun onSingleTapConfirmed(e: MotionEvent): Boolean {
                callbacks.onSingleTap()
                return true
            }

            override fun onLongPress(e: MotionEvent) {
                callbacks.onLongPress()
            }

            override fun onDoubleTap(e: MotionEvent): Boolean {
                callbacks.onDoubleTap()
                return true
            }

            override fun onScroll(e1: MotionEvent?, e2: MotionEvent, distanceX: Float, distanceY: Float): Boolean {
                if (e1 == null) return false
                val deltaY = e1.y - e2.y // 向上滑动为正
                val screenHeight = videoLayout.height.takeIf { it > 0 } ?: 1080
                
                if (adjustMode != 0) {
                    if (kotlin.math.abs(deltaY) > 20) {
                        isAdjusting = true
                    }
                    if (isAdjusting) {
                        if (adjustMode == 1) {
                            // 调节亮度
                            val change = deltaY / screenHeight
                            val lp = activity.window.attributes
                            lp.screenBrightness = (initialBrightness + change).coerceIn(0.01f, 1f)
                            activity.window.attributes = lp
                        } else if (adjustMode == 2) {
                            // 调节音量
                            val audioManager = activity.getSystemService(Context.AUDIO_SERVICE) as AudioManager
                            val maxVolume = audioManager.getStreamMaxVolume(AudioManager.STREAM_MUSIC)
                            val change = (deltaY / screenHeight) * maxVolume
                            val newVol = (initialVolume + change).toInt().coerceIn(0, maxVolume)
                            audioManager.setStreamVolume(AudioManager.STREAM_MUSIC, newVol, AudioManager.FLAG_SHOW_UI)
                        }
                        return true
                    }
                }
                return false
            }

            override fun onFling(e1: MotionEvent?, e2: MotionEvent, velocityX: Float, velocityY: Float): Boolean {
                if (e1 == null) return false
                if (isAdjusting) return false // 如果正在调亮度/音量，则不触发切台
                
                val deltaY = e2.y - e1.y
                val deltaX = e2.x - e1.x
                
                // 上下滑动：切换频道
                if (kotlin.math.abs(deltaY) > kotlin.math.abs(deltaX) && kotlin.math.abs(deltaY) > 100) {
                    if (deltaY > 0) {
                        callbacks.onSwipeDown() // 向下滑动：上一个频道
                    } else {
                        callbacks.onSwipeUp() // 向上滑动：下一个频道
                    }
                    return true
                } else if (kotlin.math.abs(deltaX) > kotlin.math.abs(deltaY) && kotlin.math.abs(deltaX) > 100) {
                    if (deltaX > 0) {
                        callbacks.onSwipeRight()
                    } else {
                        callbacks.onSwipeLeft()
                    }
                    return true
                }
                return false
            }
        })

        videoLayout.setOnTouchListener { _, event ->
            gestureDetector.onTouchEvent(event)
            true
        }
    }
}
