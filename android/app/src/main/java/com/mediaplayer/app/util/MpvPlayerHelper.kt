package com.mediaplayer.app.util

import android.content.Context
import android.net.Uri
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.view.ViewGroup
import com.mediaplayer.app.Prefs
import `is`.xyz.mpv.MPV
import `is`.xyz.mpv.MPVNode

class MpvPlayerHelper(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper, MPV.EventObserver {

    private var mpv: MPV? = null
    private var surfaceView: SurfaceView? = null
    private var isPlayerPlaying = false
    private var lastResolution = ""
    private var surfaceCreated = false
    private var isPrepared = false
    private var hasDestroyed = false

    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

    private var videoWidth = 0
    private var videoHeight = 0

    init {
        try {
            mpv = MPV()
            mpv?.create(context)
            mpv?.init()
            mpv?.addObserver(this)
            mpv?.observeProperty("time-pos", MPV.mpvFormat.MPV_FORMAT_DOUBLE)
            mpv?.observeProperty("duration", MPV.mpvFormat.MPV_FORMAT_DOUBLE)
        } catch (e: Exception) {
            e.printStackTrace()
        }

        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        currentCacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        currentDecoderMode = (context as? com.mediaplayer.app.ui.home.MainActivity)?.currentDecoderMode ?: prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
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
                    if (!hasDestroyed) {
                        mpv?.attachSurface(holder.surface)
                        if (isPlayerPlaying) {
                            mpv?.setPropertyString("pause", "no")
                        }
                    }
                }
                override fun surfaceChanged(holder: SurfaceHolder, format: Int, w: Int, h: Int) {}
                override fun surfaceDestroyed(holder: SurfaceHolder) {
                    surfaceCreated = false
                    if (!hasDestroyed) {
                        mpv?.setPropertyString("pause", "yes")
                        mpv?.detachSurface()
                    }
                }
            })
        }
        videoLayout.addView(surfaceView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String, startTimeMs: Long, contentType: String, streamType: String, channel: com.mediaplayer.app.data.model.Channel?) {
        if (hasDestroyed) return
        isWaitingForNewFile = true
        isPlayerPlaying = false
        isPrepared = false
        videoWidth = 0
        videoHeight = 0

        // SOCKS5 代理支持
        val proxyType = channel?.proxyType ?: ""
        val proxyUrl = channel?.proxyUrl ?: ""
        if (proxyType == "socks5" && proxyUrl.isNotEmpty()) {
            mpv?.setOptionString("proxy", proxyUrl)
        } else {
            mpv?.setOptionString("proxy", "")
        }

        applyPlayerOptions(url, startTimeMs)
        applyDataSource(url, userAgent, customHeaders)
    }

    private fun applyPlayerOptions(url: String, startTimeMs: Long) {
        val enableHw = currentDecoderMode == Prefs.DECODER_MODE_HARDWARE || currentDecoderMode == Prefs.DECODER_MODE_AUTO
        if (enableHw) {
            // 核心修复：摸鱼和酷9 都使用的是 mediacodec（直通渲染），而你之前用的是 mediacodec-copy。
            // mediacodec-copy 会把解码后的画面强制拷贝回 CPU 内存，这在电视盒子上是导致严重卡顿的元凶！
            mpv?.setOptionString("hwdec", "mediacodec")
            mpv?.setOptionString("hwdec-codecs", "all")
        } else {
            mpv?.setOptionString("hwdec", "no")
        }

        // 引入酷9与摸鱼的通用优化策略
        mpv?.setOptionString("profile", "fast")
        mpv?.setOptionString("vo", "gpu")
        mpv?.setOptionString("gpu-context", "android")
        mpv?.setOptionString("opengl-es", "yes")
        
        // 开启反交错，彻底解决 IPTV (1080i) 体育频道高速运动画面的横向拉丝/梳齿效应
        mpv?.setOptionString("deinterlace", "yes")
        // 软解多线程优化
        mpv?.setOptionString("vd-lavc-threads", "4")
        
        // 电视源通常是 1080i 隔行扫描，必须开启反交错，否则画面会有横纹或严重卡顿
        mpv?.setOptionString("deinterlace", "yes")
        
        // 允许视频掉帧来保证音视频同步，防止累积性卡顿
        mpv?.setOptionString("framedrop", "vo")

        mpv?.setOptionString("tls-verify", "no")

        // 区分直播流与点播/HLS流的缓存策略
        val lowerUrl = url.lowercase()
        val isHls = lowerUrl.contains(".m3u8") || lowerUrl.contains("m3u8")
        val isVod = lowerUrl.contains(".mp4") || lowerUrl.contains(".mkv") || lowerUrl.contains(".avi")
        val isLiveStream = !isHls && !isVod

        if (isLiveStream) {
            // 直播流（代理、TS、RTP/UDP组播、无后缀防盗链）
            // 核心策略：永不暂停读取 (cache-pause=no)，超大缓存 (250MB)，防止因组播或代理网络波动导致的 EOF 断流
            mpv?.setOptionString("cache", "yes")
            mpv?.setOptionString("cache-pause", "no")
            mpv?.setOptionString("demuxer-max-bytes", "250MiB")
            mpv?.setOptionString("demuxer-max-back-bytes", "50MiB")
            mpv?.setOptionString("demuxer-lavf-o", "probesize=5000000,analyzeduration=5000000") // 提升秒开速度
        } else {
            // HLS 和 VOD 点播文件
            // 核心策略：允许缓存暂停 (cache-pause=yes)，标准缓存 (128MB)，保证网络差时暂停缓冲而不是强制掉帧播放
            mpv?.setOptionString("cache", "yes")
            mpv?.setOptionString("cache-pause", "yes")
            mpv?.setOptionString("demuxer-max-bytes", "128MiB")
            mpv?.setOptionString("demuxer-max-back-bytes", "32MiB")
            mpv?.setOptionString("demuxer-lavf-o", "") // 使用默认探测
        }
        
        if (currentCacheMs > 0) {
            mpv?.setOptionString("demuxer-readahead-secs", (currentCacheMs / 1000).coerceAtLeast(1).toString())
        } else {
            mpv?.setOptionString("demuxer-readahead-secs", if (isLiveStream) "5" else "20")
        }

        if (startTimeMs > 0) {
            mpv?.setOptionString("start", (startTimeMs / 1000.0).toString())
        } else {
            mpv?.setOptionString("start", "none")
        }

        // 不强制 rtsp-transport，让 libavformat 自动协商传输协议
        mpv?.setOptionString("network-timeout", "15")
        
        applyAudioPassthrough()
    }

    private fun applyDataSource(url: String, userAgent: String, customHeaders: String) {
        try {
            val headersList = mutableListOf<String>()
            var finalUserAgent = userAgent

            if (customHeaders.isNotEmpty()) {
                try {
                    val json = org.json.JSONObject(customHeaders)
                    val keys = json.keys()
                    while (keys.hasNext()) {
                        val key = keys.next()
                        val value = json.getString(key)
                        if (key.equals("user-agent", ignoreCase = true)) {
                            finalUserAgent = value
                        } else {
                            headersList.add("$key: $value")
                        }
                    }
                } catch (e: Exception) {}
            }

            val serverUrl = com.mediaplayer.app.data.api.ApiClient.getServerUrl()
            if (url.startsWith(serverUrl)) {
                val token = com.mediaplayer.app.data.api.ApiClient.accessToken
                if (!token.isNullOrEmpty()) {
                    headersList.add("Authorization: Bearer $token")
                }
            }

            if (finalUserAgent.isNotEmpty()) {
                mpv?.setOptionString("user-agent", finalUserAgent)
            } else {
                mpv?.setOptionString("user-agent", "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0")
            }

            if (headersList.isNotEmpty()) {
                val headersString = headersList.map { 
                    if (it.contains(",")) "\"$it\"" else it 
                }.joinToString(",")
                mpv?.setOptionString("http-header-fields", headersString)
            } else {
                mpv?.setOptionString("http-header-fields", "")
            }

            val cleanUrl = url.trim()
            mpv?.command("loadfile", cleanUrl)
        } catch (e: Exception) {
            e.printStackTrace()
            listener.onError()
        }
    }

    private var audioPassthroughEnabled = false

    override fun setAudioPassthrough(enable: Boolean) {
        audioPassthroughEnabled = enable
        applyAudioPassthrough()
    }

    private fun applyAudioPassthrough() {
        if (audioPassthroughEnabled) {
            mpv?.setOptionString("audio-spdif", "ac3,eac3,dts,dts-hd,truehd")
            mpv?.setOptionString("audio-channels", "auto-safe")
        } else {
            mpv?.setOptionString("audio-spdif", "")
            mpv?.setOptionString("audio-channels", "stereo")
        }
    }

    override fun setAspectRatio(scaleMode: Int) {
        this.currentScaleMode = scaleMode
        applyScaleMode()
    }

    private fun applyScaleMode() {
        val sv = surfaceView ?: return
        
        sv.layoutParams.width = ViewGroup.LayoutParams.MATCH_PARENT
        sv.layoutParams.height = ViewGroup.LayoutParams.MATCH_PARENT
        sv.requestLayout()

        when (currentScaleMode) {
            Prefs.SCALE_MODE_DEFAULT -> {
                mpv?.setOptionString("keepaspect", "yes")
                mpv?.setOptionString("video-aspect-override", "-1")
                mpv?.setOptionString("panscan", "0.0")
            }
            Prefs.SCALE_MODE_STRETCH -> {
                mpv?.setOptionString("keepaspect", "yes")
                mpv?.setOptionString("video-aspect-override", "16:9")
                mpv?.setOptionString("panscan", "0.0")
            }
            Prefs.SCALE_MODE_16_10 -> {
                mpv?.setOptionString("keepaspect", "yes")
                mpv?.setOptionString("video-aspect-override", "16:10")
                mpv?.setOptionString("panscan", "0.0")
            }
            Prefs.SCALE_MODE_4_3 -> {
                mpv?.setOptionString("keepaspect", "yes")
                mpv?.setOptionString("video-aspect-override", "4:3")
                mpv?.setOptionString("panscan", "0.0")
            }
            Prefs.SCALE_MODE_FILL -> {
                mpv?.setOptionString("keepaspect", "no")
                mpv?.setOptionString("video-aspect-override", "-1")
                mpv?.setOptionString("panscan", "0.0")
            }
            Prefs.SCALE_MODE_CROP -> {
                mpv?.setOptionString("keepaspect", "yes")
                mpv?.setOptionString("video-aspect-override", "-1")
                mpv?.setOptionString("panscan", "1.0")
            }
        }
    }

    override fun setDecoderMode(mode: Int) {
        this.currentDecoderMode = mode
    }

    override fun setCacheDuration(cacheMs: Int) {
        this.currentCacheMs = cacheMs
    }

    override fun pause() {
        if (!hasDestroyed) {
            mpv?.setPropertyString("pause", "yes")
            isPlayerPlaying = false
        }
    }

    override fun resume() {
        if (!hasDestroyed) {
            mpv?.setPropertyString("pause", "no")
            isPlayerPlaying = true
        }
    }
    
    override fun stop() {
        if (!hasDestroyed) {
            mpv?.command("stop")
            isPlayerPlaying = false
        }
    }

    override fun isPlaying(): Boolean {
        return isPlayerPlaying
    }

    override fun getTime(): Long {
        if (hasDestroyed) return 0
        return ((mpv?.getPropertyDouble("time-pos") ?: 0.0) * 1000).toLong()
    }

    override fun getDuration(): Long {
        if (hasDestroyed) return 0
        return ((mpv?.getPropertyDouble("duration") ?: 0.0) * 1000).toLong()
    }

    override fun setTime(timeMs: Long) {
        if (!hasDestroyed) {
            mpv?.command("seek", (timeMs / 1000.0).toString(), "absolute")
        }
    }

    override fun setRate(rate: Float) {
        if (!hasDestroyed) {
            mpv?.setPropertyDouble("speed", rate.toDouble())
        }
    }

    override fun getAudioTracks(): List<AudioTrackInfo> = emptyList()
    override fun selectAudioTrack(index: Int) {}
    override fun getSubtitleTracks(): List<SubtitleTrackInfo> = emptyList()
    override fun selectSubtitleTrack(index: Int) {}
    override fun disableSubtitle() {}
    override fun loadExternalSubtitle(uri: Uri, mimeType: String): Boolean = false

    override fun release() {
        hasDestroyed = true
        mpv?.removeObserver(this)
        try {
            mpv?.command("stop")
        } catch (e: Exception) {}
        try {
            mpv?.detachSurface()
        } catch (e: Exception) {}
        videoLayout.removeView(surfaceView)
        surfaceView = null
        try {
            mpv?.destroy()
        } catch (e: Exception) {}
        mpv = null
    }

    override fun eventProperty(property: String) {}
    override fun eventProperty(property: String, value: Long) {}
    override fun eventProperty(property: String, value: Boolean) {}
    override fun eventProperty(property: String, value: String) {}
    override fun eventProperty(property: String, value: Double) {}
    override fun eventProperty(property: String, value: MPVNode) {}

    private fun formatCodecString(codec: String): String {
        return codec.split(Regex("[/(]"))[0].trim()
    }

    // 用于追踪 MPV 状态机的标识，防止切换频道时产生错误的 END_FILE 事件导致无限重连循环
    private var isWaitingForNewFile = false

    override fun event(eventId: Int, data: MPVNode) {
        when (eventId) {
            MPV.mpvEvent.MPV_EVENT_START_FILE -> {
                // MPV 已经开始加载新的文件，之前文件的任何事件都已结束
                isWaitingForNewFile = false
            }
            MPV.mpvEvent.MPV_EVENT_FILE_LOADED -> {
                isPrepared = true
                isPlayerPlaying = true
            }
            MPV.mpvEvent.MPV_EVENT_VIDEO_RECONFIG -> {
                val w = mpv?.getPropertyInt("video-params/w")
                val h = mpv?.getPropertyInt("video-params/h")
                if (w != null && h != null && w > 0 && h > 0) {
                    videoWidth = w
                    videoHeight = h
                    val vcodec = formatCodecString(mpv?.getPropertyString("video-codec") ?: "")
                    val acodec = formatCodecString(mpv?.getPropertyString("audio-codec") ?: "")
                    lastResolution = "${w}x${h}"
                    val info = buildString {
                        append(lastResolution)
                        if (vcodec.isNotEmpty()) append(" | ").append(vcodec.uppercase())
                        if (acodec.isNotEmpty()) append(" | ").append(acodec.uppercase())
                    }
                    listener.onPlaying(info)
                    videoLayout.post { applyScaleMode() }
                }
            }
            MPV.mpvEvent.MPV_EVENT_END_FILE -> {
                if (isPlayerPlaying) {
                    // 如果是在播放过程中发生了 END_FILE（比如网络断开，或者自然播完），才触发 onPlaybackCompleted
                    isPlayerPlaying = false
                    listener.onPlaybackCompleted()
                } else if (!isWaitingForNewFile) {
                    // 如果既不在播放状态，也不是在等待新文件的加载，说明 START_FILE 触发后文件加载失败了！
                    // 此时应该抛出 Error，而不是 onPlaybackCompleted（防止触发自动无限重连旧频道的 Bug）
                    listener.onError()
                } else {
                    // 这种情况属于：调用了 loadfile 切换频道，MPV 内部中止了旧频道的播放而发出的 END_FILE。
                    // 此时 isWaitingForNewFile = true，说明新频道还在路上，安全忽略该旧频道的停止事件！
                }
            }
            MPV.mpvEvent.MPV_EVENT_PLAYBACK_RESTART -> {
                listener.onBuffering(100f)
            }
        }
    }
}
