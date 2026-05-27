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
    private val listener: PlayerListener
) {
    interface PlayerListener {
        fun onBuffering(percent: Float)
        fun onPlaying(resolution: String)
        fun onError()
    }

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

        // Initial caching option, will be dynamically overriden in Media options
        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        options.add("--network-caching=$cacheMs")

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
                    listener.onPlaying(info)
                }
                MediaPlayer.Event.EncounteredError,
                MediaPlayer.Event.EndReached -> {
                    listener.onError()
                }
            }
        }
    }

    fun play(url: String, userAgent: String?, customHeaders: String?) {
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
        media.addOption(":network-caching=$cacheMs")
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

    fun setAspectRatio(ratio: String?) {
        mediaPlayer?.aspectRatio = ratio
    }

    fun isPlaying(): Boolean {
        return mediaPlayer?.isPlaying ?: false
    }

    fun pause() {
        mediaPlayer?.pause()
    }

    fun resume() {
        mediaPlayer?.play()
    }

    fun getTime(): Long {
        return mediaPlayer?.time ?: 0L
    }

    fun setTime(timeMs: Long) {
        mediaPlayer?.time = timeMs
    }

    fun getRate(): Float {
        return mediaPlayer?.rate ?: 1.0f
    }

    fun setRate(rate: Float) {
        mediaPlayer?.rate = rate
    }

    fun release() {
        mediaPlayer?.release()
        libVlc?.release()
        mediaPlayer = null
        libVlc = null
    }
}
