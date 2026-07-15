package com.mediaplayer.app.server

import fi.iki.elonen.NanoHTTPD
import okhttp3.OkHttpClient
import okhttp3.Request
import java.net.URI
import java.util.concurrent.TimeUnit
import java.util.regex.Pattern

object M3u8ProxyHandler {

    private val client = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    // 提取到 AdBlockRuleEngine.kt

    fun handle(session: NanoHTTPD.IHTTPSession): NanoHTTPD.Response {
        val params = session.parameters
        val targetUrl = params["url"]?.firstOrNull()
        
        if (targetUrl.isNullOrEmpty()) {
            return NanoHTTPD.newFixedLengthResponse(
                NanoHTTPD.Response.Status.BAD_REQUEST, 
                NanoHTTPD.MIME_PLAINTEXT, 
                "Missing url parameter"
            )
        }

        try {
            val requestBuilder = Request.Builder().url(targetUrl)
            
            // 尝试透传 User-Agent 和 Referer
            session.headers["user-agent"]?.let { ua ->
                requestBuilder.header("User-Agent", ua)
            }
            session.headers["referer"]?.let { ref ->
                requestBuilder.header("Referer", ref)
            }
            
            val request = requestBuilder.build()
            val response = client.newCall(request).execute()
            
            if (!response.isSuccessful) {
                com.mediaplayer.app.util.RemoteLogger.e("M3u8Proxy", "Failed to fetch m3u8, HTTP ${response.code}. Fail-open to original URL.")
                val res = NanoHTTPD.newFixedLengthResponse(
                    NanoHTTPD.Response.Status.REDIRECT, 
                    NanoHTTPD.MIME_HTML, 
                    "Redirecting..."
                )
                res.addHeader("Location", targetUrl)
                return res
            }

            val body = response.body?.string() ?: ""
            val hostHeader = session.headers["host"] ?: "127.0.0.1:9528"
            val localHostUrl = "http://$hostHeader"
            val cleanedBody = cleanM3u8(body, targetUrl, localHostUrl)

            val res = NanoHTTPD.newFixedLengthResponse(
                NanoHTTPD.Response.Status.OK, 
                "application/vnd.apple.mpegurl", 
                cleanedBody
            )
            res.addHeader("Access-Control-Allow-Origin", "*")
            res.addHeader("Cache-Control", "no-cache, no-store, must-revalidate")
            res.addHeader("Pragma", "no-cache")
            res.addHeader("Expires", "0")
            return res
        } catch (e: Exception) {
            e.printStackTrace()
            // Fail-Open 容灾策略：如果本地代理请求或解析发生异常，直接 302 重定向到原始点播地址
            val res = NanoHTTPD.newFixedLengthResponse(
                NanoHTTPD.Response.Status.REDIRECT, 
                NanoHTTPD.MIME_HTML, 
                "Redirecting..."
            )
            res.addHeader("Location", targetUrl)
            return res
        }
    }

    private fun cleanM3u8(originalM3u8: String, requestUri: String?, localHostUrl: String): String {
        val lines = originalM3u8.split("\n")
        val result = StringBuilder()
        var skipNextTs = false

        for (i in lines.indices) {
            val line = lines[i].trim()

            if (line.isEmpty()) continue

            if (skipNextTs && !line.startsWith("#")) {
                skipNextTs = false
                continue
            }

            if (line == "#EXT-X-DISCONTINUITY") {
                // 防止因为去广告导致出现连续的两个 #EXT-X-DISCONTINUITY，引发播放器卡顿
                val existingLines = result.toString().split("\n").filter { it.isNotBlank() }
                if (existingLines.isNotEmpty() && existingLines.last().trim() == "#EXT-X-DISCONTINUITY") {
                    com.mediaplayer.app.util.RemoteLogger.i("M3u8Proxy", "Removed redundant #EXT-X-DISCONTINUITY")
                    continue
                }
                result.append(line).append("\n")
                continue
            }

            if (line.startsWith("#EXTINF")) {
                // 提取当前切片的时长（精确匹配指纹库必备）
                val durationStr = line.substringAfter(":").substringBefore(",")
                val duration = durationStr.toDoubleOrNull()

                // 如果下一行是具体的 ts 地址，则判断是否是广告
                if (i + 1 < lines.size) {
                    val nextLine = lines[i + 1].trim()
                    if (!nextLine.startsWith("#")) {
                        if (AdBlockRuleEngine.isAdSegment(nextLine, duration)) {
                            skipNextTs = true
                            com.mediaplayer.app.util.RemoteLogger.i("M3u8Proxy", "Filtered AD segment (Regex/Hash/Duration): $nextLine")
                            continue
                        }
                    }
                }
                result.append(line).append("\n")
                continue
            }

            if (!line.startsWith("#")) {
                try {
                    val baseUri = URI(requestUri ?: "")
                    val absoluteUrl = baseUri.resolve(line).toString()
                    result.append("$localHostUrl/proxy?url=")
                        .append(java.net.URLEncoder.encode(absoluteUrl, "UTF-8"))
                        .append("\n")
                } catch (e: Exception) {
                    result.append(line).append("\n")
                }
            } else {
                result.append(line).append("\n")
            }
        }

        return result.toString()
    }

}
