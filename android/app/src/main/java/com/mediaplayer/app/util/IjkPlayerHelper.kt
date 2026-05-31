package com.mediaplayer.app.util

import android.content.Context
import android.net.Uri
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

    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

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
                    ijkPlayer?.setDisplay(holder)
                }
                override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {}
                override fun surfaceDestroyed(holder: SurfaceHolder) {
                    ijkPlayer?.setDisplay(null)
                }
            })
        }
        videoLayout.addView(surfaceView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        releasePlayer()

        ijkPlayer = IjkMediaPlayer().apply {
            // Logging
            IjkMediaPlayer.native_setLogLevel(IjkMediaPlayer.IJK_LOG_ERROR)

            // Hardware decoding
            val enableHw = currentDecoderMode == Prefs.DECODER_MODE_HARDWARE || currentDecoderMode == Prefs.DECODER_MODE_AUTO
            if (enableHw) {
                setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec", 1)
                setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec-auto-rotate", 1)
                setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec-handle-resolution-change", 1)
            } else {
                setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "mediacodec", 0)
            }

            // General optimizations
            setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "allowed_extensions", "ALL")
            setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "protocol_whitelist", "crypto,file,dash,http,https,rtp,tcp,tls,udp,rtmp,rtsp,data")
            setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "dns_cache_clear", 1)
            setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "reconnect", 1)
            setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "framedrop", 5)

            // Cache & Anti-Stutter configuration
            val activityManager = context.getSystemService(Context.ACTIVITY_SERVICE) as android.app.ActivityManager
            val memInfo = android.app.ActivityManager.MemoryInfo()
            activityManager.getMemoryInfo(memInfo)
            val maxBufBytes = minOf(memInfo.availMem / 8, 100L * 1024 * 1024)
            setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "max-buffer-size", maxBufBytes)

            // CRITICAL FIX: Restore packet-buffering to 1. Setting it to 0 drops audio/video packets on some streams causing black screen and no sound.
            setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "packet-buffering", 1)

            if (currentCacheMs > 0) {
                setOption(IjkMediaPlayer.OPT_CATEGORY_PLAYER, "max_cached_duration", currentCacheMs.toLong())
                val probeMs = Math.max(2000L, currentCacheMs.toLong() * 2)
                setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "analyzemaxduration", probeMs * 1000L)
                setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "probesize", 1024L * 1024L)
            } else {
                setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "analyzemaxduration", 100L)
                setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "probesize", 1024L * 10L)
                setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "flush_packets", 1L)
            }

            // Listeners
            setOnPreparedListener {
                it.start()
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
            setOnInfoListener { _, what, _ ->
                if (what == IMediaPlayer.MEDIA_INFO_BUFFERING_START) {
                    listener.onBuffering(0f)
                } else if (what == IMediaPlayer.MEDIA_INFO_BUFFERING_END || what == IMediaPlayer.MEDIA_INFO_VIDEO_RENDERING_START) {
                    listener.onBuffering(100f)
                }
                false
            }
            setOnErrorListener { _, _, _ ->
                listener.onError()
                true
            }
            setOnVideoSizeChangedListener { _, width, height, _, _ ->
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

            // Set Data Source
            try {
                if (userAgent.isNotEmpty() || customHeaders.isNotEmpty()) {
                    val headers = HashMap<String, String>()
                    if (userAgent.isNotEmpty()) headers["User-Agent"] = userAgent
                    
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
                    // For IjkPlayer, headers can be passed as a string
                    val headerString = headers.entries.joinToString("\r\n") { "${it.key}: ${it.value}" } + "\r\n"
                    setOption(IjkMediaPlayer.OPT_CATEGORY_FORMAT, "headers", headerString)
                }
                
                dataSource = url
                setDisplay(surfaceView?.holder)
                prepareAsync()
            } catch (e: Exception) {
                e.printStackTrace()
                listener.onError()
            }
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
        // SYNCHRONOUS release to prevent native crash
        val player = ijkPlayer
        ijkPlayer = null
        player?.apply {
            setOnPreparedListener(null)
            setOnVideoSizeChangedListener(null)
            setOnErrorListener(null)
            setOnInfoListener(null)
            setOnBufferingUpdateListener(null)
            try {
                stop()
            } catch (e: Exception) {}
            try {
                release()
            } catch (e: Exception) {}
        }
        isPlayerPlaying = false
        lastResolution = ""
    }
}
