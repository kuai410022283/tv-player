package com.mediaplayer.app.util

import android.content.Context
import android.net.Uri
import org.json.JSONObject
import org.videolan.libvlc.LibVLC
import org.videolan.libvlc.Media
import org.videolan.libvlc.MediaPlayer
import org.videolan.libvlc.interfaces.IMedia
import org.videolan.libvlc.util.VLCVideoLayout
import com.mediaplayer.app.Prefs

class VlcPlayerHelper(
    private val context: Context,
    private val videoLayout: VLCVideoLayout,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper {

    private var libVlc: LibVLC? = null
    private var mediaPlayer: MediaPlayer? = null

    init {
        initPlayer()
    }

    private fun initPlayer() {
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)

        val options = ArrayList<String>()
        options.add("--aout=opensles")
        options.add("--audio-time-stretch")
        options.add("--drop-late-frames")
        options.add("--skip-frames")
        // 强制 RTSP 使用 TCP 传输，解决 UDP 在 Android/TV 盒子环境下容易丢包或被 NAT 拦截导致无法播放的问题
        // 针对运营商 IPTV (如电信 PLTV)，服务器通常不支持 RTSP over TCP，因此先注释掉，让其走默认的 UDP
        // options.add("--rtsp-tcp")

        // We no longer add caching or jitter options globally here because they are applied per-Media based on URL in play().
        // options.add("--network-caching=$cacheMs")
        // options.add("--clock-jitter=0")
        // options.add("--clock-synchro=0")

        when (decoderMode) {
            Prefs.DECODER_MODE_HARDWARE -> {
                options.add("--avcodec-hw=any")
                options.add("--codec=mediacodec,all")
            }
            Prefs.DECODER_MODE_SOFTWARE -> {
                options.add("--avcodec-hw=none")
            }
            else -> { // AUTO
                options.add("--vout=android_display")
                options.add("--avcodec-hw=any")
            }
        }

        libVlc = LibVLC(context, options)
        mediaPlayer = MediaPlayer(libVlc)
        mediaPlayer?.attachViews(videoLayout, null, false, false)

        mediaPlayer?.setEventListener { event ->
            when (event.type) {
                MediaPlayer.Event.Buffering -> {
                    listener.onBuffering(event.buffering)
                }
                MediaPlayer.Event.Playing -> {
                    // 先发送一个基础状态
                    listener.onPlaying("VLC")
                    
                    // VLC 在刚触发 Playing 时可能还未完全解析出轨道信息，延迟 1000ms 再次读取
                    android.os.Handler(android.os.Looper.getMainLooper()).postDelayed({
                        var videoRes = ""
                        var audioCodec = ""
                        mediaPlayer?.media?.let { media ->
                            for (i in 0 until media.trackCount) {
                                val track = media.getTrack(i)
                                if (track.type == IMedia.Track.Type.Video) {
                                    val vt = track as IMedia.VideoTrack
                                    if (vt.width > 0 && vt.height > 0) {
                                        videoRes = "${vt.width}x${vt.height}"
                                    }
                                } else if (track.type == IMedia.Track.Type.Audio) {
                                    val at = track as IMedia.AudioTrack
                                    audioCodec = at.codec?.trim()?.uppercase() ?: ""
                                }
                            }
                        }
                        val info = buildString {
                            if (videoRes.isNotEmpty()) append(videoRes)
                            if (videoRes.isNotEmpty() && audioCodec.isNotEmpty()) append(" | ")
                            if (audioCodec.isNotEmpty()) append(audioCodec)
                        }
                        if (info.isNotEmpty()) {
                            listener.onPlaying(info) // 再次更新包含分辨率的状态
                        }
                    }, 1000)
                }
                MediaPlayer.Event.EncounteredError,
                MediaPlayer.Event.EndReached -> {
                    listener.onError()
                }
            }
        }
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        val player = mediaPlayer ?: return
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        val scaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)

        when (scaleMode) {
            Prefs.SCALE_MODE_STRETCH -> {
                player.aspectRatio = "16:9"
            }
            Prefs.SCALE_MODE_CROP -> {
                player.aspectRatio = null
            }
            Prefs.SCALE_MODE_4_3 -> {
                player.aspectRatio = "4:3"
            }
            else -> {
                player.aspectRatio = null
            }
        }

        val media = Media(libVlc, Uri.parse(url))
        
        // 智能缓存判断逻辑
        var finalCacheMs = cacheMs
        var useAggressiveLatency = false
        if (cacheMs == 0) { // 自动模式
            val lowerUrl = url.lowercase()
            val isLocalOrMulticast = lowerUrl.startsWith("udp://") || 
                                     lowerUrl.startsWith("rtp://") || 
                                     lowerUrl.contains("://192.168.") || 
                                     lowerUrl.contains("://10.") || 
                                     lowerUrl.contains("://172.") || 
                                     lowerUrl.contains("://180.141.") || // 典型电信IPTV
                                     lowerUrl.contains("://127.0.")
            if (isLocalOrMulticast) {
                finalCacheMs = 200 // 内网 200ms 秒切
                useAggressiveLatency = true
            } else {
                finalCacheMs = 1500 // 公网 1500ms 安全防卡
            }
        } else {
            // 如果用户手动设置了很低的缓存（<= 300ms），我们也默认开启激进模式
            useAggressiveLatency = cacheMs <= 300
        }

        media.addOption(":network-caching=$finalCacheMs")
        media.addOption(":live-caching=$finalCacheMs")
        if (useAggressiveLatency) {
            media.addOption(":clock-jitter=0")
            media.addOption(":clock-synchro=0")
        }
        
        media.addOption(":http-reconnect=true")
        if (scaleMode == Prefs.SCALE_MODE_CROP) {
            media.addOption(":crop=16:9")
        }

        val decoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        when (decoderMode) {
            Prefs.DECODER_MODE_HARDWARE -> media.setHWDecoderEnabled(true, true)
            Prefs.DECODER_MODE_SOFTWARE -> media.setHWDecoderEnabled(false, false)
            else -> media.setHWDecoderEnabled(true, false) // 自动
        }

        applyMediaOptions(media, userAgent, customHeaders)
        player.media = media
        player.play()
    }

    private fun applyMediaOptions(media: Media, userAgent: String?, customHeaders: String?) {
        if (!userAgent.isNullOrEmpty()) {
            media.addOption(":http-user-agent=$userAgent")
        }
        if (!customHeaders.isNullOrEmpty()) {
            try {
                val json = JSONObject(customHeaders)
                val keys = json.keys()
                while (keys.hasNext()) {
                    val key = keys.next()
                    val value = json.getString(key)
                    if (key.equals("referer", ignoreCase = true) || key.equals("referrer", ignoreCase = true)) {
                        media.addOption(":http-referrer=$value")
                    } else if (key.equals("origin", ignoreCase = true)) {
                        media.addOption(":http-origin=$value")
                    } else if (key.equals("cookie", ignoreCase = true)) {
                        media.addOption(":http-cookies=$value")
                    }
                }
            } catch (e: Exception) {
                e.printStackTrace()
            }
        }
    }

    override fun setAspectRatio(scaleMode: Int) {
        when (scaleMode) {
            Prefs.SCALE_MODE_STRETCH -> mediaPlayer?.aspectRatio = "16:9"
            Prefs.SCALE_MODE_CROP -> mediaPlayer?.aspectRatio = null
            Prefs.SCALE_MODE_4_3 -> mediaPlayer?.aspectRatio = "4:3"
            else -> mediaPlayer?.aspectRatio = null
        }
    }

    override fun setDecoderMode(mode: Int) {}
    override fun setCacheDuration(cacheMs: Int) {}

    override fun isPlaying(): Boolean {
        return mediaPlayer?.isPlaying ?: false
    }

    override fun pause() {
        mediaPlayer?.pause()
    }

    override fun resume() {
        mediaPlayer?.play()
    }
    
    override fun stop() {
        mediaPlayer?.stop()
    }

    override fun getTime(): Long {
        return mediaPlayer?.time ?: 0L
    }

    override fun setTime(timeMs: Long) {
        mediaPlayer?.time = timeMs
    }

    fun getRate(): Float {
        return mediaPlayer?.rate ?: 1.0f
    }

    override fun setRate(rate: Float) {
        mediaPlayer?.rate = rate
    }

    override fun release() {
        mediaPlayer?.release()
        libVlc?.release()
        mediaPlayer = null
        libVlc = null
    }
}
