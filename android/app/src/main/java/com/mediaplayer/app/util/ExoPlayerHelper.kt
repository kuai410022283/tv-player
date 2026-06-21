package com.mediaplayer.app.util

import android.content.Context
import android.net.Uri
import android.view.ViewGroup
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.VideoSize
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import androidx.media3.ui.R as media3R
import com.mediaplayer.app.Prefs

@UnstableApi
class ExoPlayerHelper(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper {

    private var exoPlayer: ExoPlayer? = null
    private var playerView: PlayerView? = null
    
    private var isPlayerPlaying = false
    private var lastResolution = ""
    
    // Config state
    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

    private var lastBuiltCacheMs: Int = -1
    private var lastBuiltDecoderMode: Int = -1
    private var lastBuiltIsLiveStream: Boolean = false

    init {
        initPlayerView()
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        currentCacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
    }

    private fun initPlayerView() {
        playerView = PlayerView(context).apply {
            useController = false
            setKeepContentOnPlayerReset(true)
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        }
        videoLayout.addView(playerView)
    }

    // Playback state for retry
    private var currentUrl: String = ""
    private var currentUserAgent: String = ""
    private var currentHeaders: String = ""
    private var isMimeTypeFallback: Boolean = false
    
    // Circuit breaker for Behind Live Window
    private var behindLiveWindowCount: Int = 0
    private var behindLiveWindowLastTime: Long = 0L

    // Circuit breaker for Audio/Video Decoder Failures
    private var criticalErrorCount: Int = 0
    private var criticalErrorLastTime: Long = 0L

    override fun play(url: String, userAgent: String, customHeaders: String) {
        currentUrl = url
        currentUserAgent = userAgent
        currentHeaders = customHeaders
        isMimeTypeFallback = false
        behindLiveWindowCount = 0
        behindLiveWindowLastTime = 0L
        criticalErrorCount = 0
        criticalErrorLastTime = 0L
        
        playInternal(url, userAgent, customHeaders, null)
    }

    private fun playInternal(url: String, userAgent: String, customHeaders: String, mimeType: String?) {
        val isLiveStream = url.lowercase().run { 
            startsWith("udp://") || startsWith("rtsp://") || startsWith("rtp://") || 
            contains("/udp/") || contains("/rtp/") || contains(".ts") || contains(".flv") 
        }
        if (exoPlayer == null || currentCacheMs != lastBuiltCacheMs || currentDecoderMode != lastBuiltDecoderMode || isLiveStream != lastBuiltIsLiveStream) {
            buildPlayer(isLiveStream)
        }

        isPlayerPlaying = false
        exoPlayer?.stop()

        applyScaleMode()

        val httpDataSourceFactory = androidx.media3.datasource.DefaultHttpDataSource.Factory()
            .setAllowCrossProtocolRedirects(true)
        if (userAgent.isNotEmpty()) {
            httpDataSourceFactory.setUserAgent(userAgent)
        }
        
        val headers = HashMap<String, String>()
        if (customHeaders.isNotEmpty()) {
            try {
                val json = org.json.JSONObject(customHeaders)
                val keys = json.keys()
                while (keys.hasNext()) {
                    val key = keys.next()
                    headers[key] = json.getString(key)
                }
            } catch (e: Exception) {}
        }
        
        // 动态添加系统 Token，防止 Token 在地址栏暴露
        val serverUrl = com.mediaplayer.app.data.api.ApiClient.getServerUrl()
        if (url.startsWith(serverUrl)) {
            val token = com.mediaplayer.app.data.api.ApiClient.accessToken
            if (!token.isNullOrEmpty()) {
                headers["Authorization"] = "Bearer $token"
            }
        }

        if (headers.isNotEmpty()) {
            httpDataSourceFactory.setDefaultRequestProperties(headers)
        }

        // 使用 DefaultDataSource.Factory 包装 HttpDataSource，
        // 这样不仅能对 HTTP/HTTPS 注入自定义头，还能完美向下兼容 file://、asset:// 等本地视频播放，防止负优化！
        val defaultDataSourceFactory = androidx.media3.datasource.DefaultDataSource.Factory(context, httpDataSourceFactory)

        // 注入自定义提取器，降低对 TS 流解析的严苛度（允许非 IDR 关键帧起播，增强容错）
        val extractorsFactory = androidx.media3.extractor.DefaultExtractorsFactory()
            .setTsExtractorFlags(androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory.FLAG_ALLOW_NON_IDR_KEYFRAMES)

        val mediaSourceFactory = DefaultMediaSourceFactory(context, extractorsFactory)
            .setDataSourceFactory(defaultDataSourceFactory)

        val mediaItemBuilder = MediaItem.Builder().setUri(Uri.parse(url))
        if (mimeType != null) {
            mediaItemBuilder.setMimeType(mimeType)
        }
        
        // 针对 UDP 或 RTSP 直播源，注入 LiveConfiguration 以优化时钟同步
        if (isLiveStream) {
            val liveConfig = MediaItem.LiveConfiguration.Builder()
                .setMaxPlaybackSpeed(1.02f)
                .build()
            mediaItemBuilder.setLiveConfiguration(liveConfig)
        }
        
        val mediaItem = mediaItemBuilder.build()
        
        // RTSP 流需要使用 RtspMediaSource，而非默认的 ProgressiveMediaSource
        val isRtsp = url.lowercase().startsWith("rtsp://")
        val mediaSource = try {
            if (isRtsp) {
                com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Creating RtspMediaSource for: $url")
                androidx.media3.exoplayer.rtsp.RtspMediaSource.Factory()
                    .setTimeoutMs(15000)
                    .setDebugLoggingEnabled(true)
                    .createMediaSource(mediaItem)
            } else {
                mediaSourceFactory.createMediaSource(mediaItem)
            }
        } catch (e: Exception) {
            com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Failed to create media source for: $url", e)
            listener.onError()
            return
        }
        
        exoPlayer?.setMediaSource(mediaSource)
        exoPlayer?.prepare()
        exoPlayer?.play()
    }

    private fun buildPlayer(isLiveStream: Boolean) {
        releasePlayer()

        lastBuiltCacheMs = currentCacheMs
        lastBuiltDecoderMode = currentDecoderMode
        lastBuiltIsLiveStream = isLiveStream

        val renderersFactory = object : DefaultRenderersFactory(context) {
            override fun buildAudioRenderers(
                context: Context,
                extensionRendererMode: Int,
                mediaCodecSelector: androidx.media3.exoplayer.mediacodec.MediaCodecSelector,
                enableDecoderFallback: Boolean,
                audioSink: androidx.media3.exoplayer.audio.AudioSink,
                eventHandler: android.os.Handler,
                eventListener: androidx.media3.exoplayer.audio.AudioRendererEventListener,
                out: java.util.ArrayList<androidx.media3.exoplayer.Renderer>
            ) {
                super.buildAudioRenderers(context, extensionRendererMode, mediaCodecSelector, enableDecoderFallback, audioSink, eventHandler, eventListener, out)
                
                if (isLiveStream) {
                    // 拦截官方生成的音频渲染器，套上我们的“防卡死代理壳”
                    for (i in 0 until out.size) {
                        out[i] = LenientAudioRendererWrapper(out[i])
                    }
                }
            }
        }.apply {
            setEnableDecoderFallback(true) // 开启解码器容错回退机制
            setExtensionRendererMode(
                when (currentDecoderMode) {
                    Prefs.DECODER_MODE_SOFTWARE -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_PREFER
                    Prefs.DECODER_MODE_HARDWARE -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_OFF
                    else -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON
                }
            )
        }

        val loadControlBuilder = DefaultLoadControl.Builder()
        if (currentCacheMs > 0) {
            loadControlBuilder.setBufferDurationsMs(
                currentCacheMs * 2,
                currentCacheMs * 4,
                currentCacheMs,
                currentCacheMs
            )
        } else {
            loadControlBuilder.setBufferDurationsMs(15000, 30000, 30, 500)
        }
        val loadControl = loadControlBuilder.build()

        val trackSelector = androidx.media3.exoplayer.trackselection.DefaultTrackSelector(context).apply {
            setParameters(buildUponParameters())
        }

        exoPlayer = ExoPlayer.Builder(context, renderersFactory)
            .setTrackSelector(trackSelector)
            .setLoadControl(loadControl)
            .build()
            
        playerView?.player = exoPlayer
        
        applyScaleMode()

        exoPlayer?.addListener(object : Player.Listener {
            override fun onPlaybackStateChanged(playbackState: Int) {
                when (playbackState) {
                    Player.STATE_BUFFERING -> {
                        listener.onBuffering(0f)
                    }
                    Player.STATE_READY -> {
                        listener.onBuffering(100f)
                        if (exoPlayer?.playWhenReady == true) {
                            if (!isPlayerPlaying) {
                                isPlayerPlaying = true
                                // 在 STATE_READY 时主动上报可用信息
                                // （onVideoSizeChanged 可能因流未上报尺寸而不触发）
                                val videoFormat = exoPlayer?.videoFormat
                                val audioFormat = exoPlayer?.audioFormat
                                val info = buildString {
                                    if (videoFormat != null && videoFormat.width > 0 && videoFormat.height > 0) {
                                        append("${videoFormat.width}x${videoFormat.height}")
                                    }
                                    val videoMime = videoFormat?.sampleMimeType?.substringAfter("/")?.uppercase()
                                    val audioMime = audioFormat?.sampleMimeType?.substringAfter("/")?.uppercase()
                                    if (!videoMime.isNullOrEmpty()) {
                                        if (isNotEmpty()) append(" | ")
                                        append(videoMime)
                                    }
                                    if (!audioMime.isNullOrEmpty()) {
                                        if (isNotEmpty()) append(" | ")
                                        append(audioMime)
                                    }
                                }
                                listener.onPlaying(info)
                            }
                        }
                    }
                    Player.STATE_ENDED -> {
                        listener.onPlaybackCompleted()
                    }
                }
            }

            override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
                com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Playback error code: ${error.errorCode}", error)
                
                // 1. 直播流滑窗越界自愈 (带熔断机制)
                if (error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW) {
                    val now = System.currentTimeMillis()
                    if (now - behindLiveWindowLastTime > 60000) {
                        behindLiveWindowCount = 0
                    }
                    behindLiveWindowCount++
                    behindLiveWindowLastTime = now
                    
                    if (behindLiveWindowCount <= 3) {
                        com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Behind live window detected ($behindLiveWindowCount/3), auto-recovering...")
                        exoPlayer?.seekToDefaultPosition()
                        exoPlayer?.prepare()
                        return // 吞掉错误，不再向上层报错
                    } else {
                        com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Behind live window circuit breaker tripped! Throwing error.")
                        // 熔断，重置计数，并抛给上层换源
                        behindLiveWindowCount = 0
                    }
                }
                
                // 2. 音视频解码或 AudioTrack 崩溃导致卡死，触发自愈重启 (兜底)
                if (error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FAILED ||
                    error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_AUDIO_TRACK_INIT_FAILED ||
                    error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_AUDIO_TRACK_WRITE_FAILED
                ) {
                    val now = System.currentTimeMillis()
                    if (now - criticalErrorLastTime > 60000) {
                        criticalErrorCount = 0
                    }
                    criticalErrorCount++
                    criticalErrorLastTime = now
                    
                    if (criticalErrorCount <= 3) {
                        com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Audio/Video critical error detected ($criticalErrorCount/3), attempting to recover...", error)
                        exoPlayer?.seekToDefaultPosition()
                        exoPlayer?.prepare()
                        return // 吞掉错误，尝试自愈
                    } else {
                        com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Critical error circuit breaker tripped! Throwing error.", error)
                        criticalErrorCount = 0
                    }
                }
                
                // 3. 格式嗅探与重试 (MimeType 降级)
                if (error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_CONTAINER_UNSUPPORTED ||
                    error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_PARSING_CONTAINER_MALFORMED ||
                    error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_IO_UNSPECIFIED
                ) {
                    if (!isMimeTypeFallback) {
                        isMimeTypeFallback = true
                        com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Parsing error detected, falling back to M3U8 MimeType...")
                        // 强制使用 M3U8 MimeType 再次尝试播放
                        playInternal(currentUrl, currentUserAgent, currentHeaders, androidx.media3.common.MimeTypes.APPLICATION_M3U8)
                        return // 吞掉本次错误
                    }
                }

                listener.onError()
            }

            override fun onVideoSizeChanged(videoSize: VideoSize) {
                if (videoSize.width > 0 && videoSize.height > 0) {
                    val audioMime = exoPlayer?.audioFormat?.sampleMimeType?.substringAfter("/")?.uppercase() ?: ""
                    val videoMime = exoPlayer?.videoFormat?.sampleMimeType?.substringAfter("/")?.uppercase() ?: ""
                    lastResolution = "${videoSize.width}x${videoSize.height}"
                    val info = buildString {
                        append(lastResolution)
                        if (videoMime.isNotEmpty()) append(" | ").append(videoMime)
                        if (audioMime.isNotEmpty()) append(" | ").append(audioMime)
                    }
                    if (isPlayerPlaying) {
                        listener.onPlaying(info)
                    }
                }
            }
        })
    }

    override fun setAspectRatio(scaleMode: Int) {
        this.currentScaleMode = scaleMode
        applyScaleMode()
    }
    
    private fun applyScaleMode() {
        val parent = videoLayout
        val pv = playerView ?: return
        val parentWidth = parent.width
        val parentHeight = parent.height
        
        if (parentWidth == 0 || parentHeight == 0) {
            // Wait for layout
            parent.post { applyScaleMode() }
            return
        }

        var targetWidth = ViewGroup.LayoutParams.MATCH_PARENT
        var targetHeight = ViewGroup.LayoutParams.MATCH_PARENT

        when (currentScaleMode) {
            Prefs.SCALE_MODE_DEFAULT -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
            }
            Prefs.SCALE_MODE_STRETCH -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
                val ratio = 16f / 9f
                val parentRatio = parentWidth.toFloat() / parentHeight.toFloat()
                if (ratio > parentRatio) {
                    targetWidth = parentWidth
                    targetHeight = (parentWidth / ratio).toInt()
                } else {
                    targetHeight = parentHeight
                    targetWidth = (parentHeight * ratio).toInt()
                }
            }
            Prefs.SCALE_MODE_16_10 -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
                val ratio = 16f / 10f
                val parentRatio = parentWidth.toFloat() / parentHeight.toFloat()
                if (ratio > parentRatio) {
                    targetWidth = parentWidth
                    targetHeight = (parentWidth / ratio).toInt()
                } else {
                    targetHeight = parentHeight
                    targetWidth = (parentHeight * ratio).toInt()
                }
            }
            Prefs.SCALE_MODE_4_3 -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
                val ratio = 4f / 3f
                val parentRatio = parentWidth.toFloat() / parentHeight.toFloat()
                if (ratio > parentRatio) {
                    targetWidth = parentWidth
                    targetHeight = (parentWidth / ratio).toInt()
                } else {
                    targetHeight = parentHeight
                    targetWidth = (parentHeight * ratio).toInt()
                }
            }
            Prefs.SCALE_MODE_FILL -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
                targetWidth = ViewGroup.LayoutParams.MATCH_PARENT
                targetHeight = ViewGroup.LayoutParams.MATCH_PARENT
            }
            Prefs.SCALE_MODE_CROP -> {
                pv.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
            }
        }

        val lp = pv.layoutParams
        if (lp is android.widget.FrameLayout.LayoutParams) {
            lp.width = targetWidth
            lp.height = targetHeight
            lp.gravity = android.view.Gravity.CENTER
            pv.layoutParams = lp
        } else {
            lp.width = targetWidth
            lp.height = targetHeight
            pv.layoutParams = lp
        }
    }

    override fun setDecoderMode(mode: Int) {
        this.currentDecoderMode = mode
    }

    override fun setCacheDuration(cacheMs: Int) {
        this.currentCacheMs = cacheMs
    }

    override fun pause() {
        exoPlayer?.pause()
    }

    override fun resume() {
        exoPlayer?.play()
    }
    
    override fun stop() {
        exoPlayer?.stop()
    }

    override fun isPlaying(): Boolean {
        return exoPlayer?.isPlaying ?: false
    }

    override fun getTime(): Long {
        return exoPlayer?.currentPosition ?: 0L
    }

    override fun setTime(timeMs: Long) {
        exoPlayer?.seekTo(timeMs)
    }

    override fun setRate(rate: Float) {
        exoPlayer?.setPlaybackSpeed(rate)
    }

    override fun release() {
        releasePlayer()
        videoLayout.removeView(playerView)
        playerView = null
    }
    
    private fun releasePlayer() {
        exoPlayer?.release()
        exoPlayer = null
        isPlayerPlaying = false
        lastResolution = ""
    }
}

// 利用 Kotlin 接口委托，零耦合代理官方渲染器，拦截音频主时钟霸权
private class LenientAudioRendererWrapper(
    private val wrappedRenderer: androidx.media3.exoplayer.Renderer
) : androidx.media3.exoplayer.Renderer by wrappedRenderer {
    
    override fun getMediaClock(): androidx.media3.exoplayer.MediaClock? {
        // 【核心补丁】强制返回 null，让 ExoPlayer 退化为使用系统物理时钟。
        // 这样即使底层 UDP 音频时间戳彻底错乱或丢包导致没有声音输出，视频画面也会匀速继续播放，绝对不会发生冻结卡死。
        return null
    }

    override fun isReady(): Boolean {
        // 如果底层音频没准备好（比如因为源流时间戳损坏导致被丢弃），我们强行让它就绪。
        // 这样 ExoPlayer 就不会为了等待音频而退回 BUFFERING 状态卡死。视频渲染器会继续渲染画面。
        return true
    }
}
