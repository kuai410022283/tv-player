package com.mediaplayer.app.core.player

import android.content.Context
import android.content.SharedPreferences
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.widget.Toast
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import org.videolan.libvlc.util.VLCVideoLayout
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.util.IPlayerHelper
import com.mediaplayer.app.util.ExoPlayerHelper
import com.mediaplayer.app.util.IjkPlayerHelper
import com.mediaplayer.app.util.VlcPlayerHelper
import com.mediaplayer.app.util.StreamResolver
import com.mediaplayer.app.util.RemoteLogger

enum class PlaybackState { IDLE, BUFFERING, PLAYING, ERROR }

sealed class PlaybackEvent {
    data class OsdUpdate(val channel: Channel, val lineIndex: Int, val linesCount: Int, val streamType: String, val coreName: String, val playCoreInt: Int) : PlaybackEvent()
    data class Error(val message: String) : PlaybackEvent()
    data class RequestSkipChannel(val isContinuousSkip: Boolean) : PlaybackEvent()
    data class StateChanged(val state: PlaybackState) : PlaybackEvent()
    data class WatchdogAlert(val message: String) : PlaybackEvent()
    data class FormatInfo(val resolution: String) : PlaybackEvent()
    data class BufferingUiUpdate(val visible: Boolean) : PlaybackEvent()
}

class PlaybackSessionManager(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val prefs: SharedPreferences,
    private val coroutineScope: CoroutineScope
) : IPlayerHelper.PlayerListener {

    private val _events = MutableSharedFlow<PlaybackEvent>()
    val events: SharedFlow<PlaybackEvent> = _events.asSharedFlow()

    private val _playbackState = MutableStateFlow(PlaybackState.IDLE)
    val playbackState: StateFlow<PlaybackState> = _playbackState.asStateFlow()

    var playerHelper: IPlayerHelper? = null
        private set

    var currentChannel: Channel? = null
        private set
    var currentLineIndex = 0
        private set
    private var coreRetryLevel = 0
    private var continuousSkipCount = 0

    private var playGeneration = 0
    private var resolveJob: Job? = null

    // Watchdog: 检测播放器冻结（缓冲超时、状态卡死）
    private val watchdogHandler = Handler(Looper.getMainLooper())
    private var isWatchdogEnabledForCurrentStream = false
    private var stateStartTime = 0L
    private var lastPlaybackTime = 0L
    private var frozenTimeCounter = 0
    private var activeDecoderMode: Int = -1
    private var lastWatchdogState: PlaybackState = PlaybackState.IDLE
    private var lastWatchdogStateTime = 0L

    private val watchdogRunnable = object : Runnable {
        override fun run() {
            val currentState = _playbackState.value
            
            // 仅在 watchdog 启用时执行检查
            if (isWatchdogEnabledForCurrentStream) {
                val now = System.currentTimeMillis()
                val stateDuration = now - stateStartTime
                val player = playerHelper
                
                when (currentState) {
                    PlaybackState.BUFFERING -> {
                        // 缓冲超过 20 秒仍无进展，视为冻结
                        if (stateDuration > 20000) {
                            RemoteLogger.i("Watchdog", "BUFFERING freeze detected: ${stateDuration}ms without progress. Triggering recovery.")
                            frozenTimeCounter++
                            // 触发错误恢复
                            coroutineScope.launch { _events.emit(PlaybackEvent.Error("播放缓冲超时 (${stateDuration/1000}s)")) }
                            player?.stop()
                            switchToNextLineOrCore()
                            // 重置计时器防止连续触发
                            stateStartTime = now
                        }
                    }
                    PlaybackState.PLAYING -> {
                        // 正在播放时不额外干预（由各播放器内部 watchdog 负责）
                    }
                    else -> {} // IDLE/ERROR 状态不处理
                }
                
                // 记录状态变化以帮助调试
                if (currentState != lastWatchdogState) {
                    RemoteLogger.i("Watchdog", "State change: ${lastWatchdogState} -> ${currentState} (enabled=${isWatchdogEnabledForCurrentStream})")
                    lastWatchdogState = currentState
                    lastWatchdogStateTime = now
                }
            }
            
            watchdogHandler.postDelayed(this, 5000) // 每 5 秒检查一次
        }
    }

    fun playChannel(channel: Channel, isAutoSkip: Boolean = false) {
        if (!isAutoSkip) {
            continuousSkipCount = 0
        }

        // Prevent redundant play
        if (channel.id == currentChannel?.id && playerHelper?.isPlaying() == true) {
            return
        }

        resolveJob?.cancel()
        resolveJob = null

        if (currentChannel?.id != channel.id) {
            currentLineIndex = 0
            coreRetryLevel = 0
        }
        currentChannel = channel
        
        prefs.edit().putLong("last_channel_id", channel.id).apply()

        playCurrentLine()
    }

    private fun playCurrentLine() {
        val channel = currentChannel ?: return
        val lines = channel.getLinesSafely()
        if (lines.isEmpty()) {
            handlePlaybackError()
            return
        }

        if (currentLineIndex >= lines.size) currentLineIndex = 0
        val line = lines[currentLineIndex]

        var globalCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        if (globalCore == 4) {
            globalCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, globalCore).apply()
        }

        var desiredCore = globalCore
        var coreText = ""

        if (globalCore == Prefs.PLAYER_CORE_AUTO) {
            if (coreRetryLevel > 0) {
                desiredCore = when (coreRetryLevel) {
                    1 -> { coreText = "智能 (VLC)"; Prefs.PLAYER_CORE_VLC }
                    2 -> { coreText = "智能 (IJK)"; Prefs.PLAYER_CORE_IJK }
                    else -> desiredCore
                }
            } else {
                desiredCore = when (line.streamType.lowercase()) {
                    "vlc" -> { coreText = "智能 (VLC)"; Prefs.PLAYER_CORE_VLC }
                    "ijk" -> { coreText = "智能 (IJK)"; Prefs.PLAYER_CORE_IJK }
                    "x5" -> { coreText = "智能 (VLC)"; Prefs.PLAYER_CORE_VLC }
                    "ts", "rtp", "udp" -> { coreText = "智能 (Exo)"; Prefs.PLAYER_CORE_EXO }
                    else -> { coreText = "智能 (Exo)"; Prefs.PLAYER_CORE_EXO }
                }
            }
        } else {
            coreText = when (desiredCore) {
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
                else -> "VLC"
            }
        }

        // 手动模式下 coreRetryLevel > 0 时不再切换到其他内核，
        // 由 switchToNextLineOrCore() 直接弹出 OSD 错误提示

        val currentPrefsDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)

        val isCoreMatch = when (desiredCore) {
            Prefs.PLAYER_CORE_EXO -> playerHelper is ExoPlayerHelper
            Prefs.PLAYER_CORE_IJK -> playerHelper is IjkPlayerHelper
            else -> playerHelper is VlcPlayerHelper
        }

        if (playerHelper == null || !isCoreMatch || activeDecoderMode != currentPrefsDecoderMode) {
            playerHelper?.release()
            videoLayout.removeAllViews()
            initPlayerWithCore(desiredCore)
            activeDecoderMode = currentPrefsDecoderMode
        } else {
            // 立即停止旧播放，防止在解析新源耗时期间旧频道继续播放（幽灵音频）
            playerHelper?.stop()
        }

        resolveJob = coroutineScope.launch {
            val gen = ++playGeneration
            
            // Emit OsdUpdate and buffering state instantly before the resolving process starts
            _events.emit(PlaybackEvent.OsdUpdate(channel, currentLineIndex, lines.size, line.streamType, coreText, desiredCore))
            updateState(PlaybackState.BUFFERING)
            
            val finalUrl = try {
                StreamResolver.resolve(line.streamUrl, line.userAgent, line.customHeaders)
            } catch (e: Exception) {
                line.streamUrl
            }

            if (gen != playGeneration) {
                RemoteLogger.i("PlaybackSessionManager", "Discarded stale play() for generation $gen")
                return@launch
            }

            val lowerUrl = finalUrl.lowercase()
            val streamTypeLower = line.streamType.lowercase()
            val isMulticastOrLive = lowerUrl.startsWith("udp://") || 
                                    lowerUrl.startsWith("rtp://") || 
                                    lowerUrl.contains(".ts") || 
                                    lowerUrl.contains(".flv") || 
                                    streamTypeLower in listOf("ts", "rtp", "udp", "flv")
            
            isWatchdogEnabledForCurrentStream = !isMulticastOrLive
            
            playerHelper?.play(finalUrl, line.userAgent, line.customHeaders)
        }

        lastPlaybackTime = 0L
        frozenTimeCounter = 0
        watchdogHandler.removeCallbacks(watchdogRunnable)
        watchdogHandler.postDelayed(watchdogRunnable, 2000)
    }

    private fun initPlayerWithCore(core: Int) {
        val aspectMode = prefs.getInt(Prefs.KEY_SCALE_MODE, 0)
        when (core) {
            Prefs.PLAYER_CORE_EXO -> {
                playerHelper = ExoPlayerHelper(context, videoLayout, this)
            }
            Prefs.PLAYER_CORE_IJK -> {
                playerHelper = IjkPlayerHelper(context, videoLayout, this)
            }
            else -> {
                val newVlcVideoLayout = org.videolan.libvlc.util.VLCVideoLayout(context)
                newVlcVideoLayout.layoutParams = android.widget.FrameLayout.LayoutParams(
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT, 
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT
                )
                videoLayout.addView(newVlcVideoLayout)
                playerHelper = VlcPlayerHelper(context, newVlcVideoLayout, this)
            }
        }
        playerHelper?.setAspectRatio(aspectMode)
    }

    private fun switchToNextLineOrCore() {
        if (currentChannel == null) return
        val lines = currentChannel?.getLinesSafely() ?: emptyList()
        val coreMode = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)

        // 手动模式（IJK/VLC/Exo）：不切换内核，直接弹出 OSD 错误提示
        if (coreMode != Prefs.PLAYER_CORE_AUTO) {
            val coreName = when (coreMode) {
                Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
                Prefs.PLAYER_CORE_VLC -> "VLC"
                Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
                else -> "播放器"
            }
            coreRetryLevel = 0
            val msg = "$coreName 播放失败，请尝试切换其他解码器"
            coroutineScope.launch { _events.emit(PlaybackEvent.Error(msg)) }
            handlePlaybackError()
            return
        }

        // 自动模式：VLC -> IJK -> Exo 顺序回退
        if (coreRetryLevel < 2) {
            coreRetryLevel++
            coroutineScope.launch { _events.emit(PlaybackEvent.Error("内核切换重试 (级别 $coreRetryLevel)...")) }
            playCurrentLine()
        } else {
            coreRetryLevel = 0
            if (lines.isNotEmpty() && currentLineIndex < lines.size - 1) {
                currentLineIndex++
                coroutineScope.launch { _events.emit(PlaybackEvent.Error("当前线路失效，尝试切换线路 ${currentLineIndex + 1}...")) }
                playCurrentLine()
            } else {
                handlePlaybackError()
            }
        }
    }

    private fun handlePlaybackError() {
        RemoteLogger.i("PlaybackSessionManager", "handlePlaybackError triggered")
        continuousSkipCount++
        if (continuousSkipCount >= 5) {
            coroutineScope.launch { _events.emit(PlaybackEvent.Error("连续 5 个频道无法播放，已停止自动换台。")) }
            stop()
            coroutineScope.launch { _events.emit(PlaybackEvent.RequestSkipChannel(isContinuousSkip = true)) }
        } else {
            coroutineScope.launch { _events.emit(PlaybackEvent.RequestSkipChannel(isContinuousSkip = false)) }
        }
    }

    override fun onVideoSizeChanged(width: Int, height: Int) {
        // 如果是第一次获取到画面尺寸，视为播放成功（某些内核可能不会准确发射 onPlaying）
        if (_playbackState.value == PlaybackState.BUFFERING) {
            updateState(PlaybackState.PLAYING)
            frozenTimeCounter = 0
            coroutineScope.launch { _events.emit(PlaybackEvent.BufferingUiUpdate(false)) }
        }
    }

    override fun onPlaying(resolution: String) {
        updateState(PlaybackState.PLAYING)
        frozenTimeCounter = 0
        coroutineScope.launch { 
            _events.emit(PlaybackEvent.BufferingUiUpdate(false))
            _events.emit(PlaybackEvent.FormatInfo(resolution))
        }
    }

    override fun onBuffering(percent: Float) {
        // 如果当前正在播放，忽略 buffering 显示，防止播放过程中因轻微波动导致 UI 频繁重绘引发 Surface 闪屏（特别是模拟器硬解下）
        if (_playbackState.value == PlaybackState.PLAYING) return

        if (percent > 0f && percent < 100f) {
            updateState(PlaybackState.BUFFERING)
        }
        coroutineScope.launch {
            // 与原版一致：0% 时隐藏（初始化状态），>=100% 时隐藏（缓冲完成），只在中间值显示
            val isVisible = percent > 0f && percent < 100f
            _events.emit(PlaybackEvent.BufferingUiUpdate(isVisible))
        }
    }

    override fun onError() {
        // 如果正在播放，忽略个别底层的容错性报错，防止中断画面
        if (_playbackState.value == PlaybackState.PLAYING && playerHelper?.isPlaying() == true) return
        
        coroutineScope.launch { _events.emit(PlaybackEvent.Error("播放错误")) }
        switchToNextLineOrCore()
    }

    fun playCatchup(url: String, userAgent: String, headers: String) {
        resolveJob?.cancel()
        playGeneration++
        
        watchdogHandler.removeCallbacks(watchdogRunnable)
        playerHelper?.release()
        continuousSkipCount = 0
        isWatchdogEnabledForCurrentStream = false
        
        initPlayerWithCore(Prefs.PLAYER_CORE_VLC) // Catchup currently hardcoded to VLC
        
        updateState(PlaybackState.BUFFERING)
        playerHelper?.play(url, userAgent, headers)
    }

    fun switchLine(lineIndex: Int) {
        val channel = currentChannel ?: return
        val lines = channel.getLinesSafely()
        if (lineIndex in lines.indices && lineIndex != currentLineIndex) {
            currentLineIndex = lineIndex
            coreRetryLevel = 0
            playCurrentLine()
        }
    }

    fun getPlayer(): IPlayerHelper? = playerHelper
    
    fun setAspectRatio(mode: Int) {
        playerHelper?.setAspectRatio(mode)
    }
    
    fun setCacheDuration(cacheMs: Int) {
        playerHelper?.setCacheDuration(cacheMs)
    }

    fun stop() {
        resolveJob?.cancel()
        watchdogHandler.removeCallbacks(watchdogRunnable)
        updateState(PlaybackState.IDLE)
        playerHelper?.release()
    }

    fun release() {
        stop()
        playerHelper = null
    }

    private fun updateState(newState: PlaybackState) {
        _playbackState.value = newState
        if (newState == PlaybackState.BUFFERING || newState == PlaybackState.PLAYING) {
            stateStartTime = System.currentTimeMillis()
        }
        coroutineScope.launch { _events.emit(PlaybackEvent.StateChanged(newState)) }
    }
}
