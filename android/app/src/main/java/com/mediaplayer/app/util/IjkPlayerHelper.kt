package com.mediaplayer.app.util

import android.content.Context
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.ViewGroup
import tv.danmaku.ijk.media.player.IMediaPlayer
import tv.danmaku.ijk.media.player.IjkMediaPlayer
import com.mediaplayer.app.Prefs

class IjkPlayerHelper(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper {

    private var ijkPlayer: IjkMediaPlayer? = null
    private var surfaceView: SurfaceView? = null
    private var isPlayerPlaying = false
    private var lastResolution = ""
    private var surfaceCreated = false

    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

    private var lastBuiltCacheMs: Int = -1
    private var lastBuiltDecoderMode: Int = -1

    init {
        try {
            IjkMediaPlayer.loadLibrariesOnce(null)
            IjkMediaPlayer.native_profileBegin("libijkplayer.so")
        } catch (e: Exception) {
            e.printStackTrace()
        }

        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        currentCacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        
        initSurfaceView()
    }

    private fun initSurfaceView() {
        surfaceView = SurfaceView(context).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )
            holder.addCallback(object : SurfaceHolder.Callback {
                override fun surfaceCreated(holder: SurfaceHolder) {
                    surfaceCreated = true
                    ijkPlayer?.let { player ->
                        player.setDisplay(holder)
                        // 如果 player 已经 prepare 好了但没有 surface，立刻重设显示
                    }
                }
                override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {}
                override fun surfaceDestroyed(holder: SurfaceHolder) {
                    surfaceCreated = false
                    ijkPlayer?.let { player ->
                        try {
                            player.setDisplay(null)
                        } catch (_: Exception) {}
                    }
                }
            })
        }
        videoLayout.addView(surfaceView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        if (ijkPlayer == null || currentCacheMs != lastBuiltCacheMs || currentDecoderMode != lastBuiltDecoderMode) {
            buildPlayer()
        }

        isPlayerPlaying = false
        ijkPlayer?.reset()
        applyPlayerOptions(ijkPlayer!!)
        applyDataSource(ijkPlayer!!, url, userAgent, customHeaders)
    }

    private fun buildPlayer() {
        releasePlayer()
        lastBuiltCacheMs = currentCacheMs
        lastBuiltDecoderMode = currentDecoderMode
        ijkPlayer = IjkMediaPlayer()
        applyPlayerOptions(ijkPlayer!!)
        if (surfaceCreated) {
            ijkPlayer?.setDisplay(surfaceView?.holder)
        }
        setupPlayerListeners(ijkPlayer!!)
    }

    private fun applyPlayerOptions(player: IjkMediaPlayer) {
        IjkMediaPlayer.native_setLogLevel(IjkMediaPlayer.IJK_LOG_ERROR)

        val enableHw = currentDecoderMode == Prefs.DECODER_MODE_HARDWARE || currentDecoderMode == Prefs.DECODER_MODE_AUTO
        if (enableHw) {
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec", 1)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec-all-videos", 1)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec-hevc", 1)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec-handle-resolution-change", 1)
        } else {
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec", 0)
        }

        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "allowed_extensions", "ALL")
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "dns_cache_clear", 1)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "dns_cache_timeout", 0)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "http-detect-range-support", 0)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "framedrop", 5)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "start-on-prepared", 1)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "opensles", 0)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "fast", 1)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "flush_packets", 1L)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "timeout", 30000L)

        if (currentCacheMs <= 0) {
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "fflags", "nobuffer")
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "packet-buffering", 0L)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "infbuf", 1L)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "min-frames", 10L)
        } else {
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "packet-buffering", 1L)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "infbuf", 0L)
            val minFrames = (currentCacheMs / 50).coerceIn(5, 60)
            player.setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "min-frames", minFrames.toLong())
        }

        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "analyzemaxduration", 500L)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "analyzeduration", 100L)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "probesize", 1024L * 100L)
        player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "max-buffer-size", 50L * 1024L * 1024L)
    }

    private fun setupPlayerListeners(player: IjkMediaPlayer) {
        player.setOnPreparedListener {
            if (surfaceCreated) {
                it.setDisplay(surfaceView?.holder)
            }
            isPlayerPlaying = true
            val audioCodec = ijkPlayer?.mediaInfo?.mAudioDecoder ?: ""
            val videoCodec = ijkPlayer?.mediaInfo?.mVideoDecoder ?: ""
            val info = buildString {
                if (lastResolution.isNotEmpty()) append(lastResolution)
                if (videoCodec.isNotEmpty()) {
                    if (isNotEmpty()) append(" | ")
                    append(videoCodec.uppercase())
                }
                if (audioCodec.isNotEmpty()) {
                    if (isNotEmpty()) append(" | ")
                    append(audioCodec.uppercase())
                }
            }
            listener.onPlaying(if (info.isNotEmpty()) info else lastResolution)
        }
        player.setOnInfoListener { _, what, _ ->
            if (what == IMediaPlayer.MEDIA_INFO_BUFFERING_START) {
                listener.onBuffering(0f)
            } else if (what == IMediaPlayer.MEDIA_INFO_BUFFERING_END || what == IMediaPlayer.MEDIA_INFO_VIDEO_RENDERING_START) {
                listener.onBuffering(100f)
            }
            false
        }
        player.setOnErrorListener { _, _, _ ->
            listener.onError()
            true
        }
        player.setOnVideoSizeChangedListener { _, width, height, _, _ ->
            if (width > 0 && height > 0) {
                val audioCodec = ijkPlayer?.mediaInfo?.mAudioDecoder ?: ""
                val videoCodec = ijkPlayer?.mediaInfo?.mVideoDecoder ?: ""
                lastResolution = "${width}x${height}"
                val info = buildString {
                    append(lastResolution)
                    if (videoCodec.isNotEmpty()) append(" | ").append(videoCodec.uppercase())
                    if (audioCodec.isNotEmpty()) append(" | ").append(audioCodec.uppercase())
                }
                if (isPlayerPlaying) {
                    listener.onPlaying(info)
                }
            }
        }
    }

    private fun applyDataSource(player: IjkMediaPlayer, url: String, userAgent: String, customHeaders: String) {
        try {
            val allHeaders = HashMap<String, String>()
            if (userAgent.isNotEmpty()) {
                player.setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "user_agent", userAgent)
            }
            if (customHeaders.isNotEmpty()) {
                try {
                    val json = org.json.JSONObject(customHeaders)
                    val keys = json.keys()
                    while (keys.hasNext()) {
                        val key = keys.next()
                        allHeaders[key] = json.getString(key)
                    }
                } catch (e: Exception) {}
            }

            if (allHeaders.isNotEmpty()) {
                player.setDataSource(url, allHeaders)
            } else {
                player.dataSource = url
            }
            player.prepareAsync()
        } catch (e: Exception) {
            e.printStackTrace()
            listener.onError()
        }
    }

    override fun setAspectRatio(scaleMode: Int) {
        this.currentScaleMode = scaleMode
        // IjkPlayer SurfaceView scaling would require wrapping in a custom MeasureLayout.
        // For simplicity in this demo, standard SurfaceView layout acts as FIT/AUTO.
    }

    override fun setDecoderMode(mode: Int) {
        this.currentDecoderMode = mode
    }

    override fun setCacheDuration(cacheMs: Int) {
        this.currentCacheMs = cacheMs
    }

    override fun pause() {
        ijkPlayer?.pause()
    }

    override fun resume() {
        ijkPlayer?.start()
    }
    
    override fun stop() {
        ijkPlayer?.stop()
    }

    override fun isPlaying(): Boolean {
        return ijkPlayer?.isPlaying ?: false
    }

    override fun getTime(): Long {
        return ijkPlayer?.currentPosition ?: 0L
    }

    override fun setTime(timeMs: Long) {
        ijkPlayer?.seekTo(timeMs)
    }

    override fun setRate(rate: Float) {
        ijkPlayer?.setSpeed(rate)
    }

    override fun release() {
        releasePlayer()
        videoLayout.removeView(surfaceView)
        surfaceView = null
        
        try {
            // Do not call native_profileEnd() here as it shuts down FFmpeg globally and causes black screen for subsequent players
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }
    
    private fun releasePlayer() {
        val player = ijkPlayer
        ijkPlayer = null
        isPlayerPlaying = false
        lastResolution = ""
        player?.apply {
            try {
                pause()
            } catch (_: Exception) {}
            try {
                stop()
            } catch (_: Exception) {}
            try {
                setDisplay(null)
            } catch (_: Exception) {}
            setOnPreparedListener(null)
            setOnVideoSizeChangedListener(null)
            setOnErrorListener(null)
            setOnInfoListener(null)
            setOnBufferingUpdateListener(null)
            try {
                reset()
            } catch (_: Exception) {}
            try {
                release()
            } catch (_: Exception) {}
        }
    }
}
