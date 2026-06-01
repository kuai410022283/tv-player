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

    private var lastUrl = ""
    private var lastUserAgent = ""
    private var lastHeaders = ""
    private var forceSoftDecodeInternal = false

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
            setKeepContentOnPlayerReset(true) // 防止 PlayerView 在平滑切流时主动清空画布（黑屏）
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        }
        videoLayout.addView(playerView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        lastUrl = url
        lastUserAgent = userAgent
        lastHeaders = customHeaders

        if (exoPlayer == null || currentCacheMs != lastBuiltCacheMs || currentDecoderMode != lastBuiltDecoderMode) {
            buildPlayer()
        }

        exoPlayer?.stop()
        
        // 针对 HLS (m3u8) 等分片直播流，强制覆盖其默认的保守延迟策略（通常会强行缓冲3个切片导致慢起播）
        val mediaItem = MediaItem.Builder()
            .setUri(Uri.parse(url))
            .setLiveConfiguration(
                MediaItem.LiveConfiguration.Builder()
                    // 强制让播放器紧贴直播时间线，避免下载多余的历史切片
                    .setTargetOffsetMs(1000)
                    .setMaxPlaybackSpeed(1.05f) // 允许轻微加速追赶直播进度
                    .build()
            )
            .build()
            
        exoPlayer?.setMediaItem(mediaItem)
        exoPlayer?.prepare()
        exoPlayer?.play()
    }

    private fun buildPlayer() {
        releasePlayer()

        lastBuiltCacheMs = currentCacheMs
        lastBuiltDecoderMode = currentDecoderMode

        // 1. Configure Decoder
        val renderersFactory = DefaultRenderersFactory(context).apply {
            // 控制音频扩展（如 FFmpeg）
            setExtensionRendererMode(
                when {
                    currentDecoderMode == Prefs.DECODER_MODE_SOFTWARE || forceSoftDecodeInternal -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_PREFER
                    currentDecoderMode == Prefs.DECODER_MODE_HARDWARE -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_OFF
                    else -> DefaultRenderersFactory.EXTENSION_RENDERER_MODE_ON
                }
            )
            
            // 【关键修复】控制视频解码（硬解/软解）
            setEnableDecoderFallback(true) // 允许解码器自动降级
            setMediaCodecSelector(
                if (currentDecoderMode == Prefs.DECODER_MODE_SOFTWARE || forceSoftDecodeInternal) {
                    // 当选择“软解”或自动回退时，强制优先使用系统 CPU 软件视频解码
                    androidx.media3.exoplayer.mediacodec.MediaCodecSelector { mimeType, requiresSecureDecoder, requiresTunnelingDecoder ->
                        val decoders = androidx.media3.exoplayer.mediacodec.MediaCodecUtil.getDecoderInfos(mimeType, requiresSecureDecoder, requiresTunnelingDecoder)
                        // 将软件解码器排在前面
                        decoders.sortedBy { it.hardwareAccelerated }.toMutableList()
                    }
                } else {
                    // 默认使用硬解
                    androidx.media3.exoplayer.mediacodec.MediaCodecSelector.DEFAULT
                }
            )
        }

        val loadControlBuilder = DefaultLoadControl.Builder()
        if (currentCacheMs > 0) {
            val safePlaybackBufMs = Math.max(40, currentCacheMs) // 极限下探至 40ms (25fps 的绝对一帧物理极限)
            val minBufMs = Math.max(1000, currentCacheMs)
            val maxBufMs = Math.max(3000, currentCacheMs * 2)
            // Restore the user's manual network cache setting effect for startup buffer, with a safety net
            loadControlBuilder.setBufferDurationsMs(minBufMs, maxBufMs, safePlaybackBufMs, safePlaybackBufMs)
        } else {
            // Default fast startup for Live TV (e.g. UDP Proxy / TS streams)
            // 将起播阈值压榨到极限的 40ms
            // 修复“出画后定屏”：卡顿恢复也降到 40ms
            loadControlBuilder.setBufferDurationsMs(1000, 3000, 40, 40)
        }
        val loadControl = loadControlBuilder.build()

        val extractorsFactory = androidx.media3.extractor.DefaultExtractorsFactory()
            .setTsExtractorFlags(
                androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory.FLAG_DETECT_ACCESS_UNITS or
                androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory.FLAG_ALLOW_NON_IDR_KEYFRAMES
            )

        val mediaSourceFactory = DefaultMediaSourceFactory(context, extractorsFactory)

        exoPlayer = ExoPlayer.Builder(context, renderersFactory)
            .setMediaSourceFactory(mediaSourceFactory)
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
                                val audioMime = exoPlayer?.audioFormat?.sampleMimeType?.substringAfter("/")?.uppercase() ?: ""
                                val videoMime = exoPlayer?.videoFormat?.sampleMimeType?.substringAfter("/")?.uppercase() ?: ""
                                val info = buildString {
                                    if (lastResolution.isNotEmpty()) append(lastResolution)
                                    if (videoMime.isNotEmpty()) {
                                        if (isNotEmpty()) append(" | ")
                                        append(videoMime)
                                    }
                                    if (audioMime.isNotEmpty()) {
                                        if (isNotEmpty()) append(" | ")
                                        append(audioMime)
                                    }
                                }
                                listener.onPlaying(if (info.isNotEmpty()) info else lastResolution)
                            }
                        }
                    }
                    Player.STATE_ENDED -> {
                        listener.onError()
                    }
                }
            }

            override fun onPlayerError(error: androidx.media3.common.PlaybackException) {
                if ((error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODER_INIT_FAILED ||
                     error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODING_FAILED ||
                     error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW) &&
                    !forceSoftDecodeInternal && currentDecoderMode != Prefs.DECODER_MODE_SOFTWARE
                ) {
                    forceSoftDecodeInternal = true
                    android.util.Log.w("ExoPlayerHelper", "Hardware decoding failed, retrying with software decoding.")
                    val pos = exoPlayer?.currentPosition ?: 0L
                    buildPlayer()
                    play(lastUrl, lastUserAgent, lastHeaders)
                    if (pos > 0) exoPlayer?.seekTo(pos)
                    return
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
        when (currentScaleMode) {
            Prefs.SCALE_MODE_STRETCH -> playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
            Prefs.SCALE_MODE_CROP -> playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
            Prefs.SCALE_MODE_4_3 -> {
                // Approximate 4:3 by using FIT if the video isn't 4:3, but Exo lacks a strict forced 4:3 mode easily.
                playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
            }
            else -> playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
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
