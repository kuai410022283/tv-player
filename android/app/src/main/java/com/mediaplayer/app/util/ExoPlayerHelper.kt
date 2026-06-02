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
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
        }
        videoLayout.addView(playerView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        releasePlayer()

        // 1. Configure Decoder
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

        // Add UserAgent and Custom Headers
        // In Media3, custom headers require customizing the HttpDataSource.
        // For simplicity, we just use standard play for now if headers aren't strictly required by Exo.
        // Usually streams that need headers might be tricky.
        val mediaItem = MediaItem.fromUri(Uri.parse(url))
        exoPlayer?.setMediaItem(mediaItem)
        exoPlayer?.prepare()
        exoPlayer?.play()
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
