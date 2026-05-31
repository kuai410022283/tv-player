package com.mediaplayer.app.util

import android.content.Context
import android.graphics.Color
import android.os.Handler
import android.os.Looper
import android.view.ViewGroup
import com.tencent.smtt.sdk.WebChromeClient
import com.tencent.smtt.sdk.WebView
import com.tencent.smtt.sdk.WebViewClient
import com.mediaplayer.app.Prefs

class X5PlayerHelper(
    private val context: Context,
    private val videoLayout: ViewGroup,
    private val listener: IPlayerHelper.PlayerListener
) : IPlayerHelper {

    private var webView: WebView? = null
    private var isPlayerPlaying = false
    private val handler = Handler(Looper.getMainLooper())

    private var currentCacheMs: Int = 0
    private var currentDecoderMode: Int = Prefs.DECODER_MODE_AUTO
    private var currentScaleMode: Int = Prefs.SCALE_MODE_DEFAULT

    init {
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        currentCacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)

        initWebView()
    }

    private fun initWebView() {
        webView = WebView(context).apply {
            setBackgroundColor(Color.BLACK)
            isFocusable = false
            isFocusableInTouchMode = false
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
            )

            settings.apply {
                javaScriptEnabled = true
                useWideViewPort = true
                loadWithOverviewMode = true
                mediaPlaybackRequiresUserGesture = false
                domStorageEnabled = true
                allowFileAccess = true
                allowContentAccess = true
            }

            webViewClient = object : WebViewClient() {
                override fun onPageFinished(view: WebView?, url: String?) {
                    super.onPageFinished(view, url)
                    listener.onBuffering(100f)
                }
            }

            webChromeClient = object : WebChromeClient() {
                // You can capture full screen intents here if needed
            }
        }
        videoLayout.addView(webView)
    }

    override fun play(url: String, userAgent: String, customHeaders: String) {
        isPlayerPlaying = false
        listener.onBuffering(0f)

        if (userAgent.isNotEmpty()) {
            webView?.settings?.userAgentString = userAgent
        }

        // Object fit determines scale mode. 
        // Default: contain (FIT)
        // Stretch: fill (STRETCH)
        // Crop: cover (CROP)
        val objectFit = when (currentScaleMode) {
            Prefs.SCALE_MODE_STRETCH -> "fill"
            Prefs.SCALE_MODE_CROP -> "cover"
            else -> "contain"
        }

        val html = """
            <!DOCTYPE html>
            <html>
            <head>
                <meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
                <style>
                    body { margin: 0; padding: 0; background-color: black; overflow: hidden; }
                    video { width: 100vw; height: 100vh; object-fit: $objectFit; outline: none; }
                </style>
            </head>
            <body>
                <video id="player" src="$url" autoplay webkit-playsinline playsinline x5-video-player-type="h5" x5-video-player-fullscreen="true"></video>
                <script>
                    var video = document.getElementById('player');
                    video.addEventListener('playing', function() {
                        window.console.log("X5Player: playing");
                    });
                    video.addEventListener('error', function() {
                        window.console.log("X5Player: error");
                    });
                </script>
            </body>
            </html>
        """.trimIndent()

        // Pass headers if needed via loadDataWithBaseURL? Custom headers are tricky in loadData, 
        // but X5 doesn't expose easy header injection for <video> tags anyway.
        webView?.loadDataWithBaseURL(url, html, "text/html", "utf-8", null)

        // Web players are hard to track accurately without JS bridges, so we simulate events
        handler.postDelayed({
            if (!isPlayerPlaying && webView != null) {
                isPlayerPlaying = true
                listener.onPlaying("WebX5")
            }
        }, 1500)
    }

    override fun setAspectRatio(scaleMode: Int) {
        this.currentScaleMode = scaleMode
    }

    override fun setDecoderMode(mode: Int) {
        this.currentDecoderMode = mode
    }

    override fun setCacheDuration(cacheMs: Int) {
        this.currentCacheMs = cacheMs
    }

    override fun pause() {
        webView?.evaluateJavascript("document.getElementById('player').pause();", null)
    }

    override fun resume() {
        webView?.evaluateJavascript("document.getElementById('player').play();", null)
    }
    
    override fun stop() {
        webView?.evaluateJavascript("document.getElementById('player').pause();", null)
    }

    override fun isPlaying(): Boolean {
        return isPlayerPlaying
    }

    override fun getTime(): Long {
        return 0L // Hard to sync without JS bridge
    }

    override fun setTime(timeMs: Long) {
        val seconds = timeMs / 1000.0
        webView?.evaluateJavascript("document.getElementById('player').currentTime = $seconds;", null)
    }

    override fun setRate(rate: Float) {
        webView?.evaluateJavascript("document.getElementById('player').playbackRate = $rate;", null)
    }

    override fun release() {
        handler.removeCallbacksAndMessages(null)
        webView?.apply {
            stopLoading()
            webChromeClient = null
            webViewClient = null
            destroy()
        }
        videoLayout.removeView(webView)
        webView = null
        isPlayerPlaying = false
    }
}
