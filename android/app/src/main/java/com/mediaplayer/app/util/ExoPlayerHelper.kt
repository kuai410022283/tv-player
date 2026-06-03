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

    override fun play(url: String, userAgent: String, customHeaders: String) {
        if (exoPlayer == null || currentCacheMs != lastBuiltCacheMs || currentDecoderMode != lastBuiltDecoderMode) {
            buildPlayer()
        }

        isPlayerPlaying = false
        exoPlayer?.stop()

        applyScaleMode()

        val httpDataSourceFactory = androidx.media3.datasource.DefaultHttpDataSource.Factory()
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
        if (headers.isNotEmpty()) {
            httpDataSourceFactory.setDefaultRequestProperties(headers)
        }

        // 使用 DefaultDataSource.Factory 包装 HttpDataSource，
        // 这样不仅能对 HTTP/HTTPS 注入自定义头，还能完美向下兼容 file://、asset:// 等本地视频播放，防止负优化！
        val defaultDataSourceFactory = androidx.media3.datasource.DefaultDataSource.Factory(context, httpDataSourceFactory)

        val mediaSourceFactory = DefaultMediaSourceFactory(context)
            .setDataSourceFactory(defaultDataSourceFactory)

        val mediaItem = MediaItem.fromUri(Uri.parse(url))
        val mediaSource = mediaSourceFactory.createMediaSource(mediaItem)
        
        exoPlayer?.setMediaSource(mediaSource)
        exoPlayer?.prepare()
        exoPlayer?.play()
    }

    private fun buildPlayer() {
        releasePlayer()

        lastBuiltCacheMs = currentCacheMs
        lastBuiltDecoderMode = currentDecoderMode

        val renderersFactory = DefaultRenderersFactory(context).apply {
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
            loadControlBuilder.setBufferDurationsMs(1500, 5000, 30, 30)
        }
        val loadControl = loadControlBuilder.build()

        exoPlayer = ExoPlayer.Builder(context, renderersFactory)
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
        // 通过内部 AspectRatioFrameLayout 控制精确比例
        val contentFrame = playerView?.findViewById<AspectRatioFrameLayout>(media3R.id.exo_content_frame)

        when (currentScaleMode) {
            Prefs.SCALE_MODE_STRETCH -> {
                playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FILL
                resetContentFrameAspectRatio(contentFrame)
                resetPlayerViewAspectRatio()
            }
            Prefs.SCALE_MODE_CROP -> {
                playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_ZOOM
                resetContentFrameAspectRatio(contentFrame)
                resetPlayerViewAspectRatio()
            }
            Prefs.SCALE_MODE_4_3 -> {
                playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                // 优先通过 AspectRatioFrameLayout.setAspectRatio() 精确控制 4:3 比例
                contentFrame?.let { frame ->
                    try {
                        val setAspectRatio = frame.javaClass.getMethod("setAspectRatio", java.lang.Float.TYPE)
                        setAspectRatio.invoke(frame, 4f / 3f)
                    } catch (_: Exception) {
                        // 降级：通过 layoutParams 强制 4:3
                        playerView?.post {
                            val parent = playerView?.parent as? ViewGroup ?: return@post
                            val targetWidth = playerView?.measuredWidth ?: parent.width
                            if (targetWidth > 0) {
                                val lp = playerView?.layoutParams
                                lp?.height = (targetWidth * 3 / 4).coerceAtLeast(1)
                                playerView?.layoutParams = lp
                            }
                        }
                    }
                }
            }
            else -> {
                playerView?.resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                resetContentFrameAspectRatio(contentFrame)
                resetPlayerViewAspectRatio()
            }
        }
    }

    /**
     * 清除 AspectRatioFrameLayout 内部 aspectRatio，恢复为视频原始比例
     * 解决 4:3 → 原始比例 切换后画面仍被强制拉伸/压缩的问题
     */
    private fun resetContentFrameAspectRatio(frame: AspectRatioFrameLayout?) {
        frame?.let { f ->
            try {
                val setAspectRatio = f.javaClass.getMethod("setAspectRatio", java.lang.Float.TYPE)
                // 使用视频的实际宽高比，而非设置为 0
                // 避免某些 Media3 版本中 aspectRatio=0 时 layout 表现异常
                val videoSize = exoPlayer?.videoSize
                if (videoSize != null && videoSize.width > 0 && videoSize.height > 0) {
                    setAspectRatio.invoke(f, videoSize.width.toFloat() / videoSize.height.toFloat())
                }
            } catch (_: Exception) {}
        }
    }

    private fun resetPlayerViewAspectRatio() {
        playerView?.post {
            val lp = playerView?.layoutParams
            lp?.height = ViewGroup.LayoutParams.MATCH_PARENT
            playerView?.layoutParams = lp
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
