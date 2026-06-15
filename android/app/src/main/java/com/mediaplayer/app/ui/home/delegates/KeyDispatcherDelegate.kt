package com.mediaplayer.app.ui.home.delegates

import android.view.KeyEvent
import android.view.View
import android.widget.TextView
import android.widget.Toast
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.ui.home.MainActivity

class KeyDispatcherDelegate(private val activity: MainActivity) {

    private var isInputtingChannel = false
    private var channelInputBuffer = java.lang.StringBuilder()
    
    // 按键防抖：同一按键 200ms 内重复触发则忽略（解决硬件连发/鬼键问题）
    private var lastKeyCode = -1
    private var lastKeyTime = 0L
    private val KEY_DEBOUNCE_MS = 200L
    
    private val channelInputRunnable = Runnable {
        isInputtingChannel = false
        val inputNum = channelInputBuffer.toString().toIntOrNull()
        channelInputBuffer.clear()
        
        val allChannels = activity.allChannels
        if (inputNum != null && inputNum > 0 && allChannels.isNotEmpty()) {
            val targetIndex = allChannels.indexOfFirst { it.globalIndex + 1 == inputNum }
            if (targetIndex != -1) {
                activity.playbackLifecycleDelegate.playTvChannel(targetIndex)
            } else {
                Toast.makeText(activity, "未找到频道: $inputNum", Toast.LENGTH_SHORT).show()
                if (activity.currentChannelIndex >= 0 && activity.currentChannelIndex < allChannels.size) {
                    val currentChannel = allChannels[activity.currentChannelIndex]
                    activity.findViewById<TextView>(R.id.tvOsdChannelNum)?.text = String.format("%03d", currentChannel.globalIndex + 1)
                    activity.findViewById<TextView>(R.id.tvOsdChannelName)?.text = currentChannel.name
                }
            }
        } else {
             if (activity.currentChannelIndex >= 0 && activity.currentChannelIndex < allChannels.size) {
                 val currentChannel = allChannels[activity.currentChannelIndex]
                 activity.findViewById<TextView>(R.id.tvOsdChannelNum)?.text = String.format("%03d", currentChannel.globalIndex + 1)
                 activity.findViewById<TextView>(R.id.tvOsdChannelName)?.text = currentChannel.name
             }
        }
    }


    fun dispatchKeyEvent(event: KeyEvent): Boolean {
        if (event.action == KeyEvent.ACTION_DOWN) {
            val keyCode = event.keyCode
            
            com.mediaplayer.app.util.RemoteLogger.i("KeyEvent", "User pressed key $keyCode")
            
            // 按键防抖：同一按键 200ms 内重复按下则忽略
            val now = System.currentTimeMillis()
            if (keyCode == lastKeyCode && (now - lastKeyTime) < KEY_DEBOUNCE_MS) {
                return true
            }
            lastKeyCode = keyCode
            lastKeyTime = now

            // 只要面板处于显示状态，用户的任何按键都应当重置自动隐藏的时间
            if (activity.findViewById<View>(R.id.layoutZappingMenu)?.visibility == View.VISIBLE) {
                activity.uiHandler.removeCallbacks(activity.hideZappingRunnable)
                activity.uiHandler.postDelayed(activity.hideZappingRunnable, 15000)
            }
            if (activity.findViewById<View>(R.id.layoutOsd)?.visibility == View.VISIBLE) {
                activity.playerOverlayDelegate.extendOsdTimeout()
            }
            
            val focusedView = activity.currentFocus
            if (focusedView != null) {
                // 跟踪用户的合法横向意图
                if (keyCode == KeyEvent.KEYCODE_DPAD_LEFT) {
                    if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvChannels))) activity.activeListArea = "groups"
                    else if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvEpgList))) activity.activeListArea = "channels"
                } else if (keyCode == KeyEvent.KEYCODE_DPAD_RIGHT) {
                    if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvGroups))) activity.activeListArea = "channels"
                    else if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvChannels))) activity.activeListArea = "epg"
                }
            }

            // 【终极 TV 焦点防御系统】
            // 拦截 Activity 级别的所有按键分发。防止极速滚动时脱离列表边界。
            if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN || keyCode == KeyEvent.KEYCODE_DPAD_UP) {
                if (focusedView != null) {
                    val direction = if (keyCode == KeyEvent.KEYCODE_DPAD_DOWN) View.FOCUS_DOWN else View.FOCUS_UP
                    val dirStr = if (direction == View.FOCUS_DOWN) "DOWN" else "UP"
                    
                    fun handleListFocus(rv: androidx.recyclerview.widget.RecyclerView, listName: String): Boolean {
                        val lm = rv.layoutManager as? androidx.recyclerview.widget.LinearLayoutManager
                        val adapter = rv.adapter
                        val focusedPos = rv.getChildAdapterPosition(focusedView)
                        val nextFocus = android.view.FocusFinder.getInstance().findNextFocus(rv as android.view.ViewGroup, focusedView, direction)
                        
                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName $dirStr | currPos:$focusedPos, nextFocusPos:${nextFocus?.let { rv.getChildAdapterPosition(it) }}, lastVisible:${lm?.findLastVisibleItemPosition()}")

                        if (nextFocus != null) {
                            nextFocus.requestFocus()
                            return true
                        } else {
                            if (lm != null && adapter != null && focusedPos != androidx.recyclerview.widget.RecyclerView.NO_POSITION) {
                                if (direction == View.FOCUS_DOWN) {
                                    val nextPos = focusedPos + 1
                                    if (nextPos < adapter.itemCount) {
                                        rv.scrollToPosition(nextPos)
                                        rv.post { lm.findViewByPosition(nextPos)?.requestFocus() }
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName Blocked escape DOWN. Snap to $nextPos")
                                    } else {
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName reached BOTTOM.")
                                    }
                                } else {
                                    val nextPos = focusedPos - 1
                                    if (nextPos >= 0) {
                                        rv.scrollToPosition(nextPos)
                                        rv.post { lm.findViewByPosition(nextPos)?.requestFocus() }
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName Blocked escape UP. Snap to $nextPos")
                                    } else {
                                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "$listName reached TOP.")
                                    }
                                }
                            }
                            return true // 始终吞噬，绝对不让 Android 全局接管焦点！
                        }
                    }

                    if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvChannels))) {
                        return handleListFocus(activity.findViewById<View>(R.id.rvChannels) as androidx.recyclerview.widget.RecyclerView, "ChannelList")
                    } else if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvGroups))) {
                        return handleListFocus(activity.findViewById<View>(R.id.rvGroups) as androidx.recyclerview.widget.RecyclerView, "GroupList")
                    } else if (activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvEpgList))) {
                        return handleListFocus(activity.findViewById<View>(R.id.rvEpgList) as androidx.recyclerview.widget.RecyclerView, "EpgList")
                    } else {
                        com.mediaplayer.app.util.RemoteLogger.i("FocusTrace", "OtherArea $dirStr | focusedId:${focusedView.id}")
                    }
                }
            }
            
            // 遥控器数字键换台
            if (activity.isTvMode && activity.findViewById<View>(R.id.layoutAuthWaiting)?.visibility == View.GONE) {
                val keyCode = event.keyCode
                if (keyCode in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 || keyCode in KeyEvent.KEYCODE_NUMPAD_0..KeyEvent.KEYCODE_NUMPAD_9) {
                    val digit = if (keyCode >= KeyEvent.KEYCODE_NUMPAD_0) {
                        keyCode - KeyEvent.KEYCODE_NUMPAD_0
                    } else {
                        keyCode - KeyEvent.KEYCODE_0
                    }
                    
                    isInputtingChannel = true
                    if (channelInputBuffer.length < 4) {
                        channelInputBuffer.append(digit)
                    }
                    
                    activity.showOsd()
                    activity.findViewById<TextView>(R.id.tvOsdChannelNum)?.text = channelInputBuffer.toString()
                    activity.findViewById<TextView>(R.id.tvOsdChannelName)?.text = "输入频道号..."
                    
                    activity.uiHandler.removeCallbacks(channelInputRunnable)
                    activity.uiHandler.postDelayed(channelInputRunnable, 1500)
                    return true
                }
            }
        } else if (event.action == KeyEvent.ACTION_UP) {
            val keyCode = event.keyCode
            if (activity.isTvMode && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                val isMenuVisible = activity.findViewById<View>(R.id.layoutZappingMenu)?.visibility == View.VISIBLE
                val isSettingsVisible = activity.findViewById<View>(R.id.layoutSettingsMenu)?.visibility == View.VISIBLE
                val isEpgVisible = activity.findViewById<View>(R.id.layoutEpgMenu)?.visibility == View.VISIBLE
                val isLineVisible = activity.findViewById<View>(R.id.layoutLineMenu)?.visibility == View.VISIBLE
                val anyPanelOpen = isMenuVisible || isSettingsVisible || isEpgVisible || isLineVisible

                if (!anyPanelOpen) {
                    // 【焦点修复】拦截焦点遗留在频道列表项上的 OK 事件，防止触发换台
                    // 改为显示 OSD（用户可通过 LEFT 键呼出频道列表）
                    val focusedView = activity.currentFocus
                    if (focusedView != null && activity.isViewDescendantOf(focusedView, activity.findViewById<View>(R.id.rvChannels))) {
                        com.mediaplayer.app.util.RemoteLogger.i("KeyEvent", "OK on channel item - intercepted for OSD")
                        activity.showOsd()
                        return true
                    }
                }
            }
        }
        return false
    }


    fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        // 任何时候按下菜单键，直接显示右侧设置
        if (keyCode == KeyEvent.KEYCODE_MENU) {
            if (event?.repeatCount == 0) {
                val isSettingsVisible = activity.findViewById<View>(R.id.layoutSettingsMenu)?.visibility == View.VISIBLE
                if (isSettingsVisible) activity.hideSettingsMenu() else activity.showSettingsMenu()
            }
            return true
        }

        if (activity.isTvMode && activity.findViewById<View>(R.id.layoutAuthWaiting)?.visibility == View.GONE) {
            val isMenuVisible = activity.findViewById<View>(R.id.layoutZappingMenu)?.visibility == View.VISIBLE
            val isSettingsVisible = activity.findViewById<View>(R.id.layoutSettingsMenu)?.visibility == View.VISIBLE
            val isEpgVisible = activity.findViewById<View>(R.id.layoutEpgMenu)?.visibility == View.VISIBLE
            val isLineVisible = activity.findViewById<View>(R.id.layoutLineMenu)?.visibility == View.VISIBLE

            val anyPanelOpen = isMenuVisible || isSettingsVisible || isEpgVisible || isLineVisible

            // 当任何面板未显示时，开始追踪 OK 键的长按事件
            if (!anyPanelOpen && (keyCode == KeyEvent.KEYCODE_DPAD_CENTER || keyCode == KeyEvent.KEYCODE_ENTER)) {
                event?.startTracking()
                return true
            }

            val reverseChannels = activity.getSharedPreferences(Prefs.FILE, android.content.Context.MODE_PRIVATE).getBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, false)
            when (keyCode) {
                KeyEvent.KEYCODE_DPAD_UP -> {
                    if (!anyPanelOpen) {
                        val targetIdx = if (reverseChannels) {
                            if (activity.currentChannelIndex < activity.allChannels.size - 1) activity.currentChannelIndex + 1 else 0
                        } else {
                            if (activity.currentChannelIndex > 0) activity.currentChannelIndex - 1 else activity.allChannels.size - 1
                        }
                        activity.playbackLifecycleDelegate.playTvChannel(targetIdx)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_DOWN -> {
                    if (!anyPanelOpen) {
                        val targetIdx = if (reverseChannels) {
                            if (activity.currentChannelIndex > 0) activity.currentChannelIndex - 1 else activity.allChannels.size - 1
                        } else {
                            if (activity.currentChannelIndex < activity.allChannels.size - 1) activity.currentChannelIndex + 1 else 0
                        }
                        activity.playbackLifecycleDelegate.playTvChannel(targetIdx)
                        return true
                    }
                }
                KeyEvent.KEYCODE_DPAD_LEFT -> {
                    if (isSettingsVisible) {
                        val focus = activity.currentFocus
                        if (focus is android.widget.SeekBar || focus is android.widget.EditText) {
                            return false
                        }

                        activity.hideSettingsMenu()
                        return true
                    }
                    if (isEpgVisible) {
                        activity.hideEpgMenu()
                        return true
                    }
                    if (isLineVisible) {
                        activity.hideLineSelectionMenu()
                        return true
                    }
                    if (!isMenuVisible) {
                        activity.showZappingMenu(focusOnGroups = false, resetToPlaying = true)
                        return true
                    }
                    // 如果 isMenuVisible 为 true，不拦截，让焦点能在菜单内部向左移动（从频道到分组）
                }
                KeyEvent.KEYCODE_DPAD_RIGHT -> {
                    if (isSettingsVisible) {
                        val focus = activity.currentFocus
                        if (focus is android.widget.SeekBar || focus is android.widget.EditText) {
                            return false
                        }

                        return true
                    }
                    if (isEpgVisible) {
                        return true
                    }
                    if (isLineVisible) {
                        activity.hideLineSelectionMenu()
                        return true
                    }
                    if (isMenuVisible) {
                        if (activity.findViewById<View>(R.id.rvChannels)?.hasFocus() == true) {
                            // 如果已经在频道列表（最右侧），再按右键则关闭菜单
                            activity.uiHandler.removeCallbacks(activity.hideZappingRunnable)
                            activity.hideZappingRunnable.run()
                            return true
                        }
                        // 如果焦点在分组列表，不拦截，让焦点能向右移动到频道列表
                    } else {
                        // 如果菜单未显示，按右键呼出完整 EPG 节目单
                        activity.showEpgMenu()
                        return true
                    }
                }
                KeyEvent.KEYCODE_BACK -> {
                    if (activity.findViewById<View>(R.id.layoutLineMenu)?.visibility == View.VISIBLE) {
                        activity.hideLineSelectionMenu()
                        return true
                    } else if (activity.findViewById<View>(R.id.layoutEpgMenu)?.visibility == View.VISIBLE) {
                        activity.hideEpgMenu()
                        return true
                    } else if (isSettingsVisible) {
                        activity.hideSettingsMenu()
                        return true
                    } else if (isMenuVisible) {
                        activity.uiHandler.removeCallbacks(activity.hideZappingRunnable)
                        activity.hideZappingRunnable.run()
                        return true
                    } else {
                        // 退出确认或者直接退出
                        activity.finish()
                        return true
                    }
                }
            }
            if (isMenuVisible && keyCode != KeyEvent.KEYCODE_DPAD_CENTER && keyCode != KeyEvent.KEYCODE_ENTER) {
                activity.uiHandler.removeCallbacks(activity.hideZappingRunnable)
                activity.uiHandler.postDelayed(activity.hideZappingRunnable, 10000)
            }
        }
        return false
    }
}
