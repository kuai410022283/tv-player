package com.mediaplayer.app.util

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject
import java.net.Inet4Address
import java.net.InetAddress
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.TimeZone
import java.util.concurrent.TimeUnit

object StreamResolver {

    // 使用不自动跟随重定向的客户端，手动接管重定向逻辑，支持 HTTPS <-> HTTP 的跨协议降级/升级
    private val client = OkHttpClient.Builder()
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .followRedirects(false)
        .followSslRedirects(false)
        .apply {
            try {
                // IPv4 优先，避免 IPv6 Happy Eyeballs 延迟
                // 部分设备（如小米电视）自定义 DNS 可能导致闪退，加 try-catch 保护
                dns(object : okhttp3.Dns {
                    override fun lookup(hostname: String): List<InetAddress> {
                        return try {
                            val all = okhttp3.Dns.SYSTEM.lookup(hostname)
                            val ipv4 = all.filter { it is Inet4Address }
                            val ipv6 = all.filter { it !is Inet4Address }
                            ipv4 + ipv6
                        } catch (e: Exception) {
                            okhttp3.Dns.SYSTEM.lookup(hostname)
                        }
                    }
                })
            } catch (e: Exception) {
                // 设备不支持 IPv4 优先策略，使用系统默认 DNS
            }
        }
        .build()

    /**
     * 根据频道原始 URL 和 EPG 时间戳生成回看 URL。
     * 采用与服务端 generateCatchupURL 一致的 URL 模式匹配逻辑（酷9实现方式），
     * 客户端直接生成回看 URL，无需请求服务端 API。
     *
     * @param streamUrl 频道原始流地址
     * @param catchupSource catchup-source 模板（M3U 中的 catchup-source 属性），为空时自动根据 URL 模式推断
     * @param startUnix 回看开始时间（Unix 秒级时间戳）
     * @param endUnix 回看结束时间（Unix 秒级时间戳）
     * @param catchupDays 回看天数限制（0 表示不限制）
     * @return 回看 URL，如果 catchupDays > 0 且 startUnix 超出范围则返回原始 URL
     */
    fun generateCatchupUrl(streamUrl: String, catchupSource: String, startUnix: Long, endUnix: Long, catchupDays: Int = 0): String {
        // catchup-days 范围校验
        if (catchupDays > 0 && startUnix > 0) {
            val earliest = System.currentTimeMillis() / 1000 - catchupDays * 86400L
            if (startUnix < earliest) {
                return streamUrl
            }
        }

        if (catchupSource.isNotEmpty()) {
            var source = catchupSource
            val startDate = Date(startUnix * 1000)
            val endDate = Date(endUnix * 1000)
            val durationSec = if (endUnix - startUnix < 0) 0 else endUnix - startUnix

            // \${} 格式（兼容旧版）
            val sdf = SimpleDateFormat("yyyyMMddHHmmss", Locale.getDefault())
            source = source.replace("\${(b)yyyyMMddHHmmss}", sdf.format(startDate))
            source = source.replace("\${(e)yyyyMMddHHmmss}", sdf.format(endDate))
            source = source.replace("\${b}", startUnix.toString())
            source = source.replace("\${e}", endUnix.toString())
            // TIMESTAMP/TIMESTAMPL 格式（xteve 预处理后）
            source = source.replace("TIMESTAMPL", endUnix.toString())
            source = source.replace("TIMESTAMP", startUnix.toString())
            // {timestamp}/{utc}/{lutc}/{duration} 格式（XC/IPTV 标准）
            // 注意：{lutc} 必须在 {utc} 之前替换，否则 {lutc} 中的 utc 部分会被误替换
            source = source.replace("{timestamp}", startUnix.toString())
            source = source.replace("{lutc}", endUnix.toString())
            source = source.replace("{utc}", startUnix.toString())
            source = source.replace("{duration}", durationSec.toString())
            // 分段日期格式 {YYYY}-{MM}-{DD}--{HH}-{mm}-{ss}
            val sdfY = SimpleDateFormat("yyyy", Locale.getDefault())
            val sdfM = SimpleDateFormat("MM", Locale.getDefault())
            val sdfD = SimpleDateFormat("dd", Locale.getDefault())
            val sdfH = SimpleDateFormat("HH", Locale.getDefault())
            val sdfMm = SimpleDateFormat("mm", Locale.getDefault())
            val sdfSs = SimpleDateFormat("ss", Locale.getDefault())
            source = source.replace("{YYYY}", sdfY.format(startDate))
            source = source.replace("{MM}", sdfM.format(startDate))
            source = source.replace("{DD}", sdfD.format(startDate))
            source = source.replace("{HH}", sdfH.format(startDate))
            source = source.replace("{mm}", sdfMm.format(startDate))
            source = source.replace("{ss}", sdfSs.format(startDate))
            // XC PHP 风格 {Y}-{m}-{d}:{H}-{M}-{S}
            source = source.replace("{Y}", sdfY.format(startDate))
            source = source.replace("{m}", sdfM.format(startDate))
            source = source.replace("{d}", sdfD.format(startDate))
            source = source.replace("{H}", sdfH.format(startDate))
            source = source.replace("{M}", sdfMm.format(startDate))
            source = source.replace("{S}", sdfSs.format(startDate))
            // {id} 占位符（XC provider）
            if (source.contains("{id}")) {
                source = source.replace("{id}", extractStreamId(streamUrl))
            }
            // 分隔符处理
            val separator = if (!source.startsWith("?") && !source.startsWith("&")) {
                if (streamUrl.contains("?")) "&" else "?"
            } else if (source.startsWith("?") && streamUrl.contains("?")) {
                source = "&" + source.substring(1)
                ""
            } else {
                ""
            }
            return streamUrl + separator + source
        }

        val u = streamUrl
        val sep = if (u.contains("?")) "&" else "?"
        val sdf = SimpleDateFormat("yyyyMMddHHmmss", Locale.getDefault())
        val startDate = Date(startUnix * 1000)
        val endDate = Date(endUnix * 1000)
        val timeStr1 = sdf.format(startDate)
        val timeStr2 = sdf.format(endDate)

        // PLTV / TVOD 模式（同 酷9 实现）
        if (u.contains("PLTV") || u.contains("TVOD")) {
            var url = u
            if (u.contains("/PLTV/")) url = url.replace("/PLTV/", "/TVOD/")
            return "$url${sep}playseek=$timeStr1-$timeStr2"
        }

        // itv.cmvideo.cn / channel-id= 模式
        if (u.contains("itv.cmvideo.cn") || u.contains("channel-id=")) {
            var url = u.replace("&livemode=1", "&livemode=4")
                .replace("000000001000", "000000002000")
            val sdf2 = SimpleDateFormat("yyyyMMdd'T'HHmmss'.00Z'", Locale.getDefault())
            val t1 = sdf2.format(startDate)
            val t2 = sdf2.format(endDate)
            return "$url${sep}starttime=$t1&endtime=$t2"
        }

        // /live/program/live/ 模式
        if (u.contains("/live/program/live/")) {
            return "$u${sep}starttime=$startUnix&endtime=$endUnix"
        }

        // /gitv/ 模式
        if (u.contains("/gitv/")) {
            val url = u.replace("live1", "lookback")
            return "$url${sep}playseek=$timeStr1-$timeStr2"
        }

        // /gitv_live/ 模式
        if (u.contains("/gitv_live/")) {
            return "$u${sep}start=$startUnix&end=$endUnix"
        }

        // ysten 模式
        if (u.contains("ysten-businessmobile") || u.contains("ysten-business")) {
            val idx = u.lastIndexOf("/")
            if (idx != -1) {
                val fileName = u.substring(idx + 1)
                val base = u.substring(0, idx).replace("live", "lookback")
                return "$base/$timeStr1/$timeStr2/$fileName"
            }
        }

        // aishang.ctlcdn 模式
        if (u.contains("aishang.ctlcdn")) {
            val url = u.replace("live", "lb")
            return "$url${sep}start=$timeStr1&end=$timeStr2"
        }

        // userid=gf001 模式
        if (u.contains("userid=gf001")) {
            val sdfUtc = SimpleDateFormat("yyyyMMddHHmmss", Locale.getDefault())
            sdfUtc.timeZone = TimeZone.getTimeZone("UTC")
            val t1 = sdfUtc.format(startDate)
            val t2 = sdfUtc.format(endDate)
            return "$u${sep}utcprogrambegin=$t1&utcprogramend=$t2"
        }

        // RTSP + AuthInfo 模式（同 酷9 实现）
        if (u.contains("rtsp") && u.contains("AuthInfo=")) {
            val sdfUtc = SimpleDateFormat("yyyyMMddHHmmss", Locale.getDefault())
            sdfUtc.timeZone = TimeZone.getTimeZone("UTC")
            val t1 = sdfUtc.format(startDate)
            val t2 = sdfUtc.format(endDate)
            return "$u${sep}playseek=$t1-$t2"
        }

        // /cms001/ 模式
        if (u.contains("/cms001/")) {
            val sdf2 = SimpleDateFormat("yyyyMMdd'T'HHmmss'.00Z'", Locale.getDefault())
            val t1 = sdf2.format(startDate)
            val t2 = sdf2.format(endDate)
            return "$u${sep}starttime=$t1&endtime=$t2"
        }

        // 未知模式，返回原始 URL
        return streamUrl
    }

    /**
     * 从 URL 路径中提取数字 ID（用于 XC provider 的 {id} 占位符），
     * 与服务端 extractStreamID 逻辑一致。
     */
    private fun extractStreamId(url: String): String {
        val parts = url.split("/")
        for (i in parts.indices.reversed()) {
            var cleaned = parts[i].removeSuffix(".m3u8")
            cleaned = cleaned.removeSuffix(".ts")
            if (cleaned.isNotEmpty() && cleaned.all { it.isDigit() }) {
                return cleaned
            }
        }
        return ""
    }

    suspend fun resolve(originalUrl: String, userAgent: String?, customHeaders: String?): String {
        return withContext(Dispatchers.IO) {
            var currentUrl = originalUrl
            if (currentUrl.startsWith("/")) {
                val serverUrl = com.mediaplayer.app.data.api.ApiClient.getServerUrl().trimEnd('/')
                currentUrl = serverUrl + currentUrl
            }

            // 代理 URL 不需要探测重定向（直接由播放器带 Token 请求），直接返回
            if (currentUrl.contains("/api/v1/stream/proxy/") ||
                currentUrl.contains("/api/v1/stream/catchup/")) {
                return@withContext currentUrl
            }

            var redirects = 0
            val maxRedirects = 5

            while (redirects < maxRedirects) {
                // 检查协程是否已被取消，及时退出避免不必要的 HTTP 请求
                if (!isActive) return@withContext currentUrl

                if (currentUrl.startsWith("file://", ignoreCase = true)) {
                    if (currentUrl.endsWith(".strm", ignoreCase = true)) {
                        try {
                            var extractedUrl: String? = null
                            val file = java.io.File(java.net.URI(currentUrl))
                            if (file.exists()) {
                                file.bufferedReader().use { reader ->
                                    var charsRead = 0
                                    var line: String? = reader.readLine()
                                    while (line != null && charsRead < 10240) {
                                        val currentLine = line ?: break
                                        charsRead += currentLine.length
                                        val trimmed = currentLine.trim()
                                        if (trimmed.startsWith("http://", ignoreCase = true) || trimmed.startsWith("https://", ignoreCase = true)) {
                                            extractedUrl = trimmed
                                            break
                                        }
                                        line = reader.readLine()
                                    }
                                }
                            }
                            if (extractedUrl != null) {
                                currentUrl = extractedUrl!!
                                redirects++
                                continue
                            } else {
                                break // 找不到链接兜底返回
                            }
                        } catch (e: Exception) {
                            e.printStackTrace()
                            break
                        }
                    } else {
                        break // 本地非 strm 文件，直接返回原始 file:// 供播放器读取
                    }
                }

                try {
                    val requestBuilder = Request.Builder().url(currentUrl)

                    val ua = if (userAgent.isNullOrEmpty()) "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 MediaPlayer-TV/1.0.0" else userAgent
                    requestBuilder.header("User-Agent", ua)

                    if (!customHeaders.isNullOrEmpty()) {
                        try {
                            val json = JSONObject(customHeaders)
                            val keys = json.keys()
                            while (keys.hasNext()) {
                                val key = keys.next()
                                val value = json.getString(key)
                                requestBuilder.header(key, value)
                            }
                        } catch (e: Exception) {
                            e.printStackTrace()
                        }
                    }

                    // 使用 GET 请求，因为有些服务端会拦截 HEAD 请求。
                    // 只要在读取到响应头后立即调用 response.close()，就不会下载响应体，不会浪费带宽。
                    requestBuilder.get()

                    val response = client.newCall(requestBuilder.build()).execute()
                    
                    // HTTP 请求返回后检查协程是否已被取消
                    if (!isActive) {
                        response.close()
                        return@withContext currentUrl
                    }
                    
                    val code = response.code
                    val isRedirect = code in 300..399

                    if (isRedirect) {
                        val location = response.header("Location")
                        response.close()
                        if (!location.isNullOrEmpty()) {
                            currentUrl = if (location.startsWith("http://", ignoreCase = true) || location.startsWith("https://", ignoreCase = true)) {
                                location
                            } else {
                                val baseUri = java.net.URI(currentUrl)
                                baseUri.resolve(location).toString()
                            }
                            redirects++
                            continue
                        } else {
                            break
                        }
                    } else {
                        // 如果是 .strm 文件且请求成功，尝试解析提取真实的视频直链
                        if (code == 200 && currentUrl.endsWith(".strm", ignoreCase = true)) {
                            var extractedUrl: String? = null
                            try {
                                response.body?.byteStream()?.let { stream ->
                                    val reader = java.io.BufferedReader(java.io.InputStreamReader(stream))
                                    var charsRead = 0
                                    var line: String? = reader.readLine()
                                    while (line != null && charsRead < 10240) { // 最多读取 ~10KB 防止内存爆炸
                                        val currentLine = line ?: break
                                        charsRead += currentLine.length
                                        val trimmed = currentLine.trim()
                                        if (trimmed.startsWith("http://", ignoreCase = true) || trimmed.startsWith("https://", ignoreCase = true)) {
                                            extractedUrl = trimmed
                                            break
                                        }
                                        line = reader.readLine()
                                    }
                                }
                            } catch (e: Exception) {
                                e.printStackTrace()
                            }
                            response.close()
                            
                            if (extractedUrl != null) {
                                currentUrl = extractedUrl!!
                                redirects++
                                continue
                            }
                        } else {
                            response.close()
                        }
                        
                        // 遇到非重定向状态（如 200 OK），说明这就是真实的流地址
                        break
                    }
                } catch (e: Exception) {
                    e.printStackTrace()
                    break // 发生异常时直接中断，返回目前获取到的地址
                }
            }
            currentUrl
        }
    }
}
