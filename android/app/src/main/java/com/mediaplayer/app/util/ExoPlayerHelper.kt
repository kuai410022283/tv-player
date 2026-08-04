package com.mediaplayer.app.util

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.view.ViewGroup
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.VideoSize
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.DefaultRenderersFactory
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import androidx.media3.ui.R as media3R
import com.mediaplayer.app.MediaPlayerApp
import com.mediaplayer.app.Prefs

@UnstableApi
class ExoPlayerHelper(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper {

    private var exoPlayer: ExoPlayer? = null
    private var playerView: PlayerView? = null
    private var bandwidthMeter: androidx.media3.exoplayer.upstream.DefaultBandwidthMeter? = null
    
    private var isPlayerPlaying = false
    private var lastResolution = ""
    
    // Config state
    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

    private var lastBuiltCacheMs: Int = -1
    private var lastBuiltDecoderMode: Int = -1
    private var lastBuiltIsLive: Boolean = false

    // 复用 MediaSourceFactory（保留认证头等配置，用于外挂字幕加载）
    private var mediaSourceFactory: DefaultMediaSourceFactory? = null

    // 缓存 ExtractorsFactory，避免每次切换频道都重建（避免重复创建）
    private var cachedExtractorsFactory: androidx.media3.extractor.ExtractorsFactory? = null

    // 性能追踪：记录 playInternal 开始时间
    private var playStartTimeMs: Long = 0L

    init {
        initPlayerView()
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        currentCacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        currentDecoderMode = (context as? com.mediaplayer.app.ui.home.MainActivity)?.currentDecoderMode ?: prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
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
    private var currentContentType: String = ""
    private var currentStreamType: String = ""
    private var isMimeTypeFallback: Boolean = false
    // SOCKS5 代理状态（用于重试时保持代理配置）
    private var currentProxyType: String = ""
    private var currentProxyUrl: String = ""
    
    // Circuit breaker for Behind Live Window
    private var behindLiveWindowCount: Int = 0
    private var behindLiveWindowLastTime: Long = 0L

    // Circuit breaker for Audio/Video Decoder Failures
    private var criticalErrorCount: Int = 0
    private var criticalErrorLastTime: Long = 0L

    // 解码器初始化失败时的一次性软解降级标记
    // 每次 play() 调用重置，确保每个新频道/新内容独立判断
    private var decoderInitFallbackTriggered: Boolean = false

    // 缓冲超时兜底：ExoPlayer 卡在 STATE_BUFFERING 超过 15 秒自动触发降级
    // 解决某些 RTSP 或异常流不报错也不就绪的卡死问题
    private val bufferingTimeoutHandler = android.os.Handler(android.os.Looper.getMainLooper())
    private val bufferingTimeoutRunnable = Runnable {
        com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Buffering timeout! No STATE_READY within 15s, triggering fallback.")
        listener.onError()
    }

    override fun play(url: String, userAgent: String, customHeaders: String, startTimeMs: Long, contentType: String, streamType: String, channel: com.mediaplayer.app.data.model.Channel?) {
        currentUrl = url
        currentUserAgent = userAgent
        currentHeaders = customHeaders
        currentContentType = contentType
        currentStreamType = streamType
        currentProxyType = channel?.proxyType ?: ""
        currentProxyUrl = channel?.proxyUrl ?: ""
        isMimeTypeFallback = false
        behindLiveWindowCount = 0
        behindLiveWindowLastTime = 0L
        criticalErrorCount = 0
        criticalErrorLastTime = 0L
        decoderInitFallbackTriggered = false
        
        playInternal(url, userAgent, customHeaders, null, startTimeMs, contentType, streamType)
    }

    private fun playInternal(originalUrl: String, userAgent: String, customHeaders: String, mimeType: String?, startTimeMs: Long = 0L, contentType: String = "", streamType: String = "") {
        playStartTimeMs = android.os.SystemClock.uptimeMillis()
        var url = originalUrl
        val lowerUrl = url.lowercase()

        // isLiveStream 判断优先级：
        // 1. contentType (管理员手动设置: "live"/"vod")
        // 2. streamType (后端协议类型: "ts"/"rtp"/"udp"=直播, "mp4"/"mkv"=点播)
        // 3. URL 推断（兜底，排除代理 URL 避免误判）
        val st = streamType.lowercase().trim()
        val ct = contentType.lowercase().trim()
        val isLiveStream = when {
            ct == "live" -> true
            ct == "vod" -> false
            st in listOf("rtp", "udp", "rtsp", "rtmp") -> true
            st in listOf("mp4", "mkv", "avi", "mov", "webm") -> false
            st == "flv" -> true
            st == "ts" -> !lowerUrl.contains("/stream/proxy/")  // 代理TS流按点播处理
            st == "hls" -> false  // HLS 默认按点播（缓冲更稳），如需直播由管理员设 content_type=live
            else -> lowerUrl.run {
                startsWith("udp://") || startsWith("rtsp://") || startsWith("rtp://") || contains("/udp/") || contains("/rtp/")
            }
        }
        RemoteLogger.i("ExoPlayer", "isLiveStream=$isLiveStream (streamType=$st, contentType=$ct, url=${url.take(80)})")

        // 直连流走本地 Go 代理
        // RTSP：ExoPlayer 原生 RtspMediaSource 对部分非标 RTSP 服务器兼容性有限，
        // 通过本地 Go 代理（gortsplib）转 HTTP 后播放更可靠
        // RTP/UDP/IGMP：ExoPlayer 无法直接处理，必须经 Go 代理转 HTTP
        // 仅当 ExoPlayer 播放时生效，MPV 原生支持这些协议无需代理
        if (MediaPlayerApp.isProxyEnabled && MediaPlayerApp.localProxyPort > 0) {
            val originalLower = originalUrl.lowercase()
            if (originalLower.startsWith("udp://") || originalLower.startsWith("rtp://") ||
                originalLower.startsWith("igmp://") || originalLower.startsWith("rtsp://")) {
                val proxyUrl = "http://127.0.0.1:${MediaPlayerApp.localProxyPort}/proxy?url=${Uri.encode(originalUrl)}"
                RemoteLogger.i("ExoPlayer", "直连流走本地代理: ${proxyUrl.take(80)}")
                url = proxyUrl
            }
        }

        // 每次起播前探测一下当前电视/盒子的 HDR 体质
        HdrCapabilitiesHelper.printHdrInfo(context)

        val needRebuild = exoPlayer == null || currentCacheMs != lastBuiltCacheMs || currentDecoderMode != lastBuiltDecoderMode || isLiveStream != lastBuiltIsLive
        if (needRebuild) {
            val buildStart = android.os.SystemClock.uptimeMillis()
            buildPlayer(isLiveStream)
            RemoteLogger.i("ExoPlayer", "[Perf] buildPlayer took ${android.os.SystemClock.uptimeMillis() - buildStart}ms")
        }

        isPlayerPlaying = false

        // 每次起播重置缓冲超时
        bufferingTimeoutHandler.removeCallbacks(bufferingTimeoutRunnable)

        // 读取切台模式设置
        // 流畅模式（默认）：stop + clearMediaItems，保留 Surface 旧帧（setKeepContentOnPlayerReset）
        // 兼容模式：额外分离-重挂 Surface，更兼容但有短暂黑屏
        val compatibleMode = context.getSharedPreferences(Prefs.FILE, android.content.Context.MODE_PRIVATE)
            .getBoolean(Prefs.KEY_STOP_PREVIOUS_MEDIA, false)

        // 两种模式都需要 stop + clearMediaItems，确保解码器状态彻底重置
        // setKeepContentOnPlayerReset(true) 会在 stop 时保留旧帧
        exoPlayer?.stop()
        exoPlayer?.clearMediaItems()

        if (compatibleMode) {
            // 兼容模式：额外分离 Surface，后续重挂
            exoPlayer?.clearVideoSurface()
        }

        applyScaleMode()

        val okHttpClient = com.mediaplayer.app.util.PlayerNetworkHelper.getPlayerOkHttpClient(currentProxyType, currentProxyUrl)
        val okHttpDataSourceFactory = androidx.media3.datasource.okhttp.OkHttpDataSource.Factory(okHttpClient)
        
        if (userAgent.isNotEmpty()) {
            okHttpDataSourceFactory.setUserAgent(userAgent)
        } else {
            okHttpDataSourceFactory.setUserAgent("Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0")
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
            okHttpDataSourceFactory.setDefaultRequestProperties(headers)
        }

        // 使用 DefaultDataSource.Factory 包装 OkHttpDataSource，
        // 这样不仅能对 HTTP/HTTPS 注入自定义头，还能完美向下兼容 file://、asset:// 等本地视频播放，防止负优化！
        val defaultDataSourceFactory = androidx.media3.datasource.DefaultDataSource.Factory(context, okHttpDataSourceFactory)

        // 缓存 ExtractorsFactory，只创建一次，避免每次切换频道都重建
        if (cachedExtractorsFactory == null) {
            val defaultExtractorsFactory = androidx.media3.extractor.DefaultExtractorsFactory()
                .setTsExtractorFlags(androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory.FLAG_ALLOW_NON_IDR_KEYFRAMES)
                .setTsExtractorTimestampSearchBytes(androidx.media3.extractor.ts.TsExtractor.DEFAULT_TIMESTAMP_SEARCH_BYTES * 10)

            cachedExtractorsFactory = androidx.media3.extractor.ExtractorsFactory {
                val extractors = defaultExtractorsFactory.createExtractors()
                val newExtractors = mutableListOf<androidx.media3.extractor.Extractor>()
                for (extractor in extractors) {
                    if (extractor is androidx.media3.extractor.ts.TsExtractor) {
                        // 替换原生的 TsExtractor，注入 Av3aTsPayloadReaderFactory 以支持 AV3A 音频解码
                        newExtractors.add(androidx.media3.extractor.ts.TsExtractor(
                            androidx.media3.extractor.ts.DefaultTsPayloadReaderFactory.FLAG_ALLOW_NON_IDR_KEYFRAMES,
                            androidx.media3.common.util.TimestampAdjuster(0),
                            androidx.media3.extractor.ts.Av3aTsPayloadReaderFactory()
                        ))
                    } else if (extractor is androidx.media3.extractor.mp4.Mp4Extractor) {
                        // 替换原生的 Mp4Extractor，注入支持 av3a 的提取器
                        newExtractors.add(com.mediaplayer.app.extractor.mp4.Mp4Extractor())
                    } else if (extractor is androidx.media3.extractor.mp4.FragmentedMp4Extractor) {
                        // 替换 fMP4 提取器
                        newExtractors.add(com.mediaplayer.app.extractor.mp4.FragmentedMp4Extractor())
                    } else {
                        newExtractors.add(extractor)
                    }
                }
                newExtractors.toTypedArray()
            }
        }
        val extractorsFactory = cachedExtractorsFactory!!

        val mediaSourceFactory = DefaultMediaSourceFactory(context, extractorsFactory)
            .setDataSourceFactory(defaultDataSourceFactory)
        this.mediaSourceFactory = mediaSourceFactory

        val mediaItem = if (url.lowercase().startsWith("rtsp://")) {
            // RTSP 流：使用纯净 MediaItem，不附加 LiveConfiguration
            // LiveConfiguration 为 HLS/DASH 设计，RtspMediaSource 无法正确处理会导致连接挂起
            MediaItem.fromUri(Uri.parse(url))
        } else {
            val builder = MediaItem.Builder().setUri(Uri.parse(url))
            if (mimeType != null) {
                builder.setMimeType(mimeType)
            }
            if (isLiveStream) {
                val liveConfig = MediaItem.LiveConfiguration.Builder()
                    .setMaxPlaybackSpeed(1.02f)
                    .build()
                builder.setLiveConfiguration(liveConfig)
            }
            builder.build()
        }
        
        // RTSP 流需要使用 RtspMediaSource，HLS 流需使用带有 Av3aHlsExtractorFactory 的 HlsMediaSource
        val lowerUrlForType = url.lowercase()
        val isRtsp = lowerUrlForType.startsWith("rtsp://")
        val isHls = lowerUrlForType.contains(".m3u8") || mimeType == androidx.media3.common.MimeTypes.APPLICATION_M3U8 || ct == "hls" || st == "hls"
        
        val mediaSource = try {
            if (isRtsp) {
                com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Creating RtspMediaSource for: $url")
                val rtspUserAgent = if (userAgent.isNotEmpty()) userAgent else "okhttp/4.12.0"
                androidx.media3.exoplayer.rtsp.RtspMediaSource.Factory()
                    .setUserAgent(rtspUserAgent)
                    .setTimeoutMs(15000)
                    .setForceUseRtpTcp(true)
                    .setDebugLoggingEnabled(true)
                    .createMediaSource(mediaItem)
            } else if (isHls) {
                com.mediaplayer.app.util.RemoteLogger.i("ExoPlayer", "Creating HlsMediaSource with Av3aHlsExtractorFactory for: $url")
                androidx.media3.exoplayer.hls.HlsMediaSource.Factory(defaultDataSourceFactory)
                    .setExtractorFactory(androidx.media3.extractor.ts.Av3aHlsExtractorFactory())
                    .createMediaSource(mediaItem)
            } else {
                mediaSourceFactory.createMediaSource(mediaItem)
            }
        } catch (e: Exception) {
            com.mediaplayer.app.util.RemoteLogger.e("ExoPlayer", "Failed to create media source for: $url", e)
            listener.onError()
            return
        }
        
        // 兼容模式：重挂 Surface（前面已分离）
        // 流畅模式：跳过，Surface 未分离，无需重挂
        if (compatibleMode) {
            playerView?.player = null
            playerView?.player = exoPlayer
        }

        val now = android.os.SystemClock.uptimeMillis()
        RemoteLogger.i("ExoPlayer", "[Perf] MediaSource created in ${now - playStartTimeMs}ms (needRebuild=$needRebuild)")

        // 使用 setMediaSource(源, startPositionMs) 将进度与媒体源绑定，
        // 这比 setMediaSource()+seekTo() 更可靠：ExoPlayer 在 prepare 过程中会直接从该位置开始解领。
        if (startTimeMs > 0L) {
            exoPlayer?.setMediaSource(mediaSource, startTimeMs)
        } else {
            exoPlayer?.setMediaSource(mediaSource)
        }
        
        exoPlayer?.prepare()
        exoPlayer?.play()
        RemoteLogger.i("ExoPlayer", "[Perf] prepare+play issued in ${android.os.SystemClock.uptimeMillis() - playStartTimeMs}ms total")
    }

    private fun buildPlayer(isLiveStream: Boolean = false) {
        releasePlayer()

        lastBuiltCacheMs = currentCacheMs
        lastBuiltDecoderMode = currentDecoderMode
        lastBuiltIsLive = isLiveStream

        val prefs = context.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, Context.MODE_PRIVATE)
        val isPassthroughEnabled = prefs.getBoolean(com.mediaplayer.app.Prefs.KEY_AUDIO_PASSTHROUGH, false)
        val enableAv3aTvStereoSafety = !isPassthroughEnabled

        val renderersFactory = object : DefaultRenderersFactory(context) {
            override fun buildAudioRenderers(
                context: Context,
                extensionRendererMode: Int,
                mediaCodecSelector: androidx.media3.exoplayer.mediacodec.MediaCodecSelector,
                enableDecoderFallback: Boolean,
                audioSink: androidx.media3.exoplayer.audio.AudioSink,
                eventHandler: Handler,
                eventListener: androidx.media3.exoplayer.audio.AudioRendererEventListener,
                out: java.util.ArrayList<androidx.media3.exoplayer.Renderer>
            ) {
                // 原生的 Renderers
                super.buildAudioRenderers(context, extensionRendererMode, mediaCodecSelector, enableDecoderFallback, audioSink, eventHandler, eventListener, out)
                
                // 注入 Av3aAudioRenderer 以支持 AV3A 音频解码
                // 设计意图：此处使用 out.add() 显式追加，独立于 extensionRendererMode 控制。
                // 即使 DECODER_MODE_HARDWARE (EXTENSION_RENDERER_MODE_OFF) 下，
                // super() 不添加 FFmpeg 渲染器，Av3aAudioRenderer 依然始终注入。
                // 原因：MediaCodecAudioRenderer 不识别 "audio/av3a"，
                // 若不强制注入，AV3A 内容在 HARDWARE 模式下将无法播放（静音）。
                out.add(androidx.media3.decoder.av3a.Av3aAudioRenderer(eventHandler, eventListener, audioSink, enableAv3aTvStereoSafety))
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

        // 根据设备实际可用内存动态分配缓冲区上限
        val maxMemory = Runtime.getRuntime().maxMemory()
        val maxSafeBuffer = maxMemory / 3
        val targetBuffer = Math.min(250L * 1024 * 1024, maxSafeBuffer).toInt()

        val loadControlBuilder = DefaultLoadControl.Builder()
            .setTargetBufferBytes(targetBuffer)
            .setBackBuffer(0, true) // 切台时保留前一帧，避免黑屏

        if (isLiveStream) {
            // 直播流（TS/UDP/RTSP）：激进策略，低延迟优先
            loadControlBuilder
                .setPrioritizeTimeOverSizeThresholds(false)
                .setBufferDurationsMs(0, 60000, 0, 0)
        } else if (currentCacheMs > 0) {
            // 点播流 + 用户自定义缓冲：适中策略
            loadControlBuilder
                .setPrioritizeTimeOverSizeThresholds(false)
                .setBufferDurationsMs(
                    currentCacheMs * 2,
                    currentCacheMs * 4,
                    1000,
                    2000
                )
        } else {
            // 点播流 + 默认缓冲：适中策略，兼顾起播速度和 Seek 稳定性
            loadControlBuilder
                .setPrioritizeTimeOverSizeThresholds(false)
                .setBufferDurationsMs(5000, 60000, 1000, 2000)
        }
        val loadControl = loadControlBuilder.build()

        val trackSelector = androidx.media3.exoplayer.trackselection.DefaultTrackSelector(context).apply {
            setParameters(buildUponParameters()
                // 允许自动切换视频分辨率以匹配电视面板
                .setAllowVideoMixedMimeTypeAdaptiveness(true)
            )
        }

        val audioAttributes = androidx.media3.common.AudioAttributes.Builder()
            .setUsage(androidx.media3.common.C.USAGE_MEDIA)
            .setContentType(androidx.media3.common.C.AUDIO_CONTENT_TYPE_MOVIE)
            .setSpatializationBehavior(
                if (isPassthroughEnabled) androidx.media3.common.C.SPATIALIZATION_BEHAVIOR_AUTO
                else androidx.media3.common.C.SPATIALIZATION_BEHAVIOR_NEVER
            )
            .build()

        bandwidthMeter = androidx.media3.exoplayer.upstream.DefaultBandwidthMeter.Builder(context).build()

        exoPlayer = ExoPlayer.Builder(context, renderersFactory)
            .setTrackSelector(trackSelector)
            .setLoadControl(loadControl)
            .setAudioAttributes(audioAttributes, true)
            .setBandwidthMeter(bandwidthMeter!!)
            .build()
            
        playerView?.player = exoPlayer
        
        applyScaleMode()

        exoPlayer?.addListener(exoPlayerListener)
    }

    private val exoPlayerListener = object : Player.Listener {
        override fun onPlaybackStateChanged(playbackState: Int) {
            when (playbackState) {
                Player.STATE_BUFFERING -> {
                    listener.onBuffering(0f)
                    RemoteLogger.i("ExoPlayer", "[Perf] STATE_BUFFERING +${android.os.SystemClock.uptimeMillis() - playStartTimeMs}ms")
                    // 缓冲超时兜底：15秒后未就绪触发降级
                    bufferingTimeoutHandler.removeCallbacks(bufferingTimeoutRunnable)
                    bufferingTimeoutHandler.postDelayed(bufferingTimeoutRunnable, 15000)
                }
                Player.STATE_READY -> {
                    // 取消缓冲超时
                    bufferingTimeoutHandler.removeCallbacks(bufferingTimeoutRunnable)
                    listener.onBuffering(100f)
                    if (exoPlayer?.playWhenReady == true) {
                        if (!isPlayerPlaying) {
                            isPlayerPlaying = true
                            val readyMs = android.os.SystemClock.uptimeMillis() - playStartTimeMs
                            RemoteLogger.i("ExoPlayer", "[Perf] STATE_READY in ${readyMs}ms (first frame)")
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

                            val vMime = videoFormat?.sampleMimeType ?: ""
                            val aMime = audioFormat?.sampleMimeType ?: ""
                            val colorInfo = videoFormat?.colorInfo
                            
                            val isHdr10 = colorInfo?.colorTransfer == androidx.media3.common.C.COLOR_TRANSFER_ST2084
                            val isHlg = colorInfo?.colorTransfer == androidx.media3.common.C.COLOR_TRANSFER_HLG
                            val isDolbyVision = vMime == "video/dolby-vision" || videoFormat?.codecs?.contains("dvh1") == true || videoFormat?.codecs?.contains("dvhe") == true
                            val isDolbyAtmos = aMime == "audio/eac3-joc" || audioFormat?.codecs?.contains("joc") == true
                            val isDolbyAudio = !isDolbyAtmos && (aMime.contains("ac3") || aMime.contains("eac3") || aMime == "audio/true-hd" || audioFormat?.codecs?.contains("ac-3", ignoreCase = true) == true)
                            val isDts = aMime.contains("dts") || audioFormat?.codecs?.contains("dts", ignoreCase = true) == true
                            
                            val badgeInfo = com.mediaplayer.app.util.StreamBadgeInfo(
                                isDolbyVision = isDolbyVision,
                                isHdr10 = isHdr10,
                                isHlg = isHlg,
                                isDolbyAtmos = isDolbyAtmos,
                                isDolbyAudio = isDolbyAudio,
                                isDts = isDts,
                                audioCodec = aMime.substringAfter("/").uppercase(),
                                videoCodec = vMime.substringAfter("/").uppercase()
                            )
                            listener.onMediaInfoReady(badgeInfo)

                            // 通知轨道列表变化
                            listener.onTracksChanged(
                                audioTracks = getAudioTracks(),
                                subtitleTracks = getSubtitleTracks()
                            )
                        }
                    }
                }
                Player.STATE_ENDED -> {
                    // 取消缓冲超时
                    bufferingTimeoutHandler.removeCallbacks(bufferingTimeoutRunnable)
                    listener.onPlaybackCompleted()
                    // 点播流播放结束后，自动回到开头重新播放，避免黑屏
                    if (currentContentType == "vod" || currentStreamType.lowercase() in listOf("mp4", "mkv", "avi", "mov", "webm")) {
                        exoPlayer?.seekTo(0)
                        exoPlayer?.play()
                    }
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
            
            // 2. 解码器初始化/查询失败 → 一次性自动切换到软解模式 (仅对 HARDWARE 模式生效)
            if ((error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODER_INIT_FAILED ||
                 error.errorCode == androidx.media3.common.PlaybackException.ERROR_CODE_DECODER_QUERY_FAILED) &&
                currentDecoderMode == Prefs.DECODER_MODE_HARDWARE &&
                !decoderInitFallbackTriggered
            ) {
                decoderInitFallbackTriggered = true
                currentDecoderMode = Prefs.DECODER_MODE_AUTO
                lastBuiltDecoderMode = -1
                com.mediaplayer.app.util.RemoteLogger.e(
                    "ExoPlayer",
                    "Decoder init failed in HARDWARE mode. Auto-fallback to AUTO (software) mode."
                )
                playInternal(
                    currentUrl, currentUserAgent, currentHeaders,
                    null, 0L, currentContentType, currentStreamType
                )
                return
            }

            // 3. 音视频解码或 AudioTrack 崩溃导致卡死，触发自愈重启 (兜底)
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
                    playInternal(currentUrl, currentUserAgent, currentHeaders, androidx.media3.common.MimeTypes.APPLICATION_M3U8, contentType = currentContentType, streamType = currentStreamType)
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

    override fun getDuration(): Long {
        val d = exoPlayer?.duration ?: 0L
        return if (d == androidx.media3.common.C.TIME_UNSET) 0L else d
    }

    override fun getBandwidth(): Long {
        val bitsPerSec = bandwidthMeter?.bitrateEstimate ?: 0L
        return if (bitsPerSec > 0) bitsPerSec / 8 else 0L
    }

    override fun setTime(timeMs: Long) {
        exoPlayer?.seekTo(timeMs)
    }

    override fun setRate(rate: Float) {
        exoPlayer?.setPlaybackSpeed(rate)
    }

    // ── 音轨/字幕接口实现 ──

    override fun getAudioTracks(): List<AudioTrackInfo> {
        val tracks = exoPlayer?.currentTracks ?: return emptyList()
        val result = mutableListOf<AudioTrackInfo>()
        for ((groupIndex, group) in tracks.groups.withIndex()) {
            if (group.type == C.TRACK_TYPE_AUDIO) {
                for (i in 0 until group.length) {
                    val format = group.getTrackFormat(i)
                    result.add(AudioTrackInfo(
                        index = (groupIndex shl 16) or (i and 0xFFFF),
                        language = format.language ?: "und",
                        label = format.label ?: languageToDisplayName(format.language)
                            ?: "音轨 ${result.size + 1}",
                        codec = format.sampleMimeType?.substringAfter("/")?.uppercase() ?: "",
                        channelCount = format.channelCount,
                        isSelected = group.isTrackSelected(i)
                    ))
                }
            }
        }
        return result
    }

    override fun selectAudioTrack(index: Int) {
        val exo = exoPlayer ?: return
        val groupIndex = index ushr 16
        val trackIndex = index and 0xFFFF
        val groups = exo.currentTracks.groups
        if (groupIndex in groups.indices) {
            val group = groups[groupIndex]
            if (group.type == C.TRACK_TYPE_AUDIO) {
                val builder = exo.trackSelectionParameters.buildUpon()
                    .clearOverridesOfType(C.TRACK_TYPE_AUDIO)
                
                for (g in groups) {
                    if (g.type == C.TRACK_TYPE_AUDIO) {
                        if (g == group) {
                            builder.addOverride(TrackSelectionOverride(g.mediaTrackGroup, listOf(trackIndex)))
                        } else {
                            builder.addOverride(TrackSelectionOverride(g.mediaTrackGroup, emptyList<Int>()))
                        }
                    }
                }
                
                exo.trackSelectionParameters = builder.build()
            }
        }
    }

    override fun getSubtitleTracks(): List<SubtitleTrackInfo> {
        val tracks = exoPlayer?.currentTracks ?: return emptyList()
        val result = mutableListOf(SubtitleTrackInfo(
            index = -1, language = "", label = "关闭",
            isEmbedded = false, mimeType = "", isSelected = true
        ))
        var trackCounter = 1
        for ((groupIndex, group) in tracks.groups.withIndex()) {
            if (group.type == C.TRACK_TYPE_TEXT) {
                for (i in 0 until group.length) {
                    val format = group.getTrackFormat(i)
                    val isSelected = group.isTrackSelected(i)
                    if (isSelected) result[0] = result[0].copy(isSelected = false)
                    result.add(SubtitleTrackInfo(
                        index = (groupIndex shl 16) or (i and 0xFFFF),
                        language = format.language ?: "und",
                        label = format.label ?: languageToDisplayName(format.language)
                            ?: "字幕 $trackCounter",
                        isEmbedded = true,
                        mimeType = format.sampleMimeType ?: "",
                        isSelected = isSelected
                    ))
                    trackCounter++
                }
            }
        }
        return result
    }

    override fun selectSubtitleTrack(index: Int) {
        val exo = exoPlayer ?: return
        if (index < 0) { disableSubtitle(); return }
        
        val groupIndex = index ushr 16
        val trackIndex = index and 0xFFFF
        val groups = exo.currentTracks.groups
        if (groupIndex in groups.indices) {
            val group = groups[groupIndex]
            if (group.type == C.TRACK_TYPE_TEXT) {
                val builder = exo.trackSelectionParameters.buildUpon()
                    .clearOverridesOfType(C.TRACK_TYPE_TEXT)
                    
                for (g in groups) {
                    if (g.type == C.TRACK_TYPE_TEXT) {
                        if (g == group) {
                            builder.addOverride(TrackSelectionOverride(g.mediaTrackGroup, listOf(trackIndex)))
                        } else {
                            builder.addOverride(TrackSelectionOverride(g.mediaTrackGroup, emptyList<Int>()))
                        }
                    }
                }
                
                exo.trackSelectionParameters = builder.build()
            }
        }
    }

    override fun disableSubtitle() {
        val exo = exoPlayer ?: return
        exo.trackSelectionParameters = exo.trackSelectionParameters
            .buildUpon()
            .setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            .build()
    }

    override fun loadExternalSubtitle(uri: Uri, mimeType: String): Boolean {
        val exo = exoPlayer ?: return false
        val currentPos = exo.currentPosition
        val wasPlaying = exo.isPlaying

        val originalItem = exo.currentMediaItem ?: return false
        val subtitleConfig = MediaItem.SubtitleConfiguration.Builder(uri)
            .setMimeType(mimeType)
            .setSelectionFlags(C.SELECTION_FLAG_DEFAULT)
            .build()

        val newItem = originalItem.buildUpon()
            .setSubtitleConfigurations(
                originalItem.localConfiguration?.subtitleConfigurations.orEmpty() + subtitleConfig
            )
            .build()

        val factory = mediaSourceFactory ?: return false
        val mediaSource = factory.createMediaSource(newItem)
        exo.setMediaSource(mediaSource)
        exo.seekTo(currentPos)
        exo.prepare()
        if (wasPlaying) exo.play()
        return true
    }

    override fun release() {
        releasePlayer()
        videoLayout.removeView(playerView)
        playerView = null
    }
    
    private fun releasePlayer() {
        // 清理缓冲超时
        bufferingTimeoutHandler.removeCallbacks(bufferingTimeoutRunnable)
        exoPlayer?.release()
        exoPlayer = null
        isPlayerPlaying = false
        lastResolution = ""
    }
}

// 辅助：语言代码 → 中文显示名
private fun languageToDisplayName(code: String?): String? {
    return when (code?.take(2)) {
        "zh", "chi", "zho" -> "中文"
        "en", "eng" -> "英语"
        "ja", "jpn" -> "日语"
        "ko", "kor" -> "韩语"
        "fr", "fre", "fra" -> "法语"
        "de", "deu", "ger" -> "德语"
        "es", "spa" -> "西班牙语"
        "pt", "por" -> "葡萄牙语"
        "ru", "rus" -> "俄语"
        "ar", "ara" -> "阿拉伯语"
        "th", "tha" -> "泰语"
        "vi", "vie" -> "越南语"
        else -> code?.uppercase()
    }
}
