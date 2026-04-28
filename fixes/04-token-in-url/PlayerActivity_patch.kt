// ========================================
// PlayerActivity.kt — 修改 playStream 方法
// 使用 Header 传递 Token
// ========================================

// ★ 修改 playStream 方法中的 URL 构建和 DataSourceFactory 配置

private fun playStream(url: String, type: String) {
    val player = player ?: return
    retryCount = 0
    progressBar?.visibility = View.VISIBLE
    tvChannelName?.text = channelName
    tvStreamType?.text = type.uppercase()

    // ★ 修改：不再在 URL 中拼接 token，统一通过 Header 传递
    val streamUrl = if (url.startsWith("http")) {
        // 如果是代理 URL，使用代理地址（不含 token）
        if (url.contains(ApiClient.getBaseUrl().removeSuffix("/api/v1/"))) {
            url
        } else {
            ApiClient.getStreamProxyUrl(channelId)
        }
    } else {
        url
    }

    val mediaItem = MediaItem.fromUri(streamUrl)

    // ★ 修改：在 DataSourceFactory 中通过 Header 传递 Token
    val token = authManager.getToken()
    val dataSourceFactory = DefaultHttpDataSource.Factory()
        .setUserAgent("TVPlayer/1.0")
        .setConnectTimeoutMs(10000)
        .setReadTimeoutMs(15000)
        .apply {
            if (token != null) {
                setDefaultRequestProperties(
                    mapOf(
                        "X-Client-Token" to token,
                        "Authorization" to "Bearer $token"
                    )
                )
            }
        }

    val mediaSource = when (type.lowercase()) {
        "hls" -> HlsMediaSource.Factory(dataSourceFactory).createMediaSource(mediaItem)
        "dash" -> DashMediaSource.Factory(dataSourceFactory).createMediaSource(mediaItem)
        "rtsp" -> RtspMediaSource.Factory().createMediaSource(mediaItem)
        "flv", "mp4" -> ProgressiveMediaSource.Factory(dataSourceFactory).createMediaSource(mediaItem)
        else -> HlsMediaSource.Factory(dataSourceFactory).createMediaSource(mediaItem)
    }

    player.setMediaSource(mediaSource)
    player.prepare()
    player.playWhenReady = true
}
