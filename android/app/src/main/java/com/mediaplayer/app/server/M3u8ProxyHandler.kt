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

    // 常见的广告特征正则：匹配包含 /ad/、advert、p3p. 等常见切片广告域名的行
    private val AD_URL_PATTERN = Pattern.compile("(?i)(/ad/|advert|promo|p3p\\.)")

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

    private fun cleanM3u8(content: String, baseUriString: String, localHostUrl: String): String {
        val lines = content.split("\n")
        val result = StringBuilder()
        var skipNextTs = false
        val baseUri = URI(baseUriString)

        for (i in lines.indices) {
            var line = lines[i].trim()
            if (line.isEmpty()) continue

            // 如果当前行是 #EXTINF，我们往下探测一行看看是不是广告 ts
            if (line.startsWith("#EXTINF")) {
                if (i + 1 < lines.size) {
                    val nextLine = lines[i + 1].trim()
                    if (!nextLine.startsWith("#")) {
                        // 这是一个真实的 ts 切片地址
                        if (isAdSegment(nextLine)) {
                            // 发现广告，跳过 EXTINF 行，并且标记跳过下一行的 ts
                            skipNextTs = true
                            com.mediaplayer.app.util.RemoteLogger.i("M3u8Proxy", "Filtered AD segment: $nextLine")
                            continue
                        }
                    }
                }
            } else if (!line.startsWith("#")) {
                // 处理 ts/m3u8 链接行
                if (skipNextTs) {
                    skipNextTs = false
                    continue
                }
                
                // 将相对路径补全为绝对路径，这样播放器可以直接去源站拉 ts，不用全部通过本地代理中转
                try {
                    val absoluteUrl = baseUri.resolve(line).toString()
                    if (absoluteUrl.contains(".m3u8")) {
                        // 如果是 Master Playlist 嵌套的 m3u8，继续走代理清洗
                        val encoded = java.net.URLEncoder.encode(absoluteUrl, "UTF-8")
                        line = "$localHostUrl/proxy/m3u8?url=$encoded"
                    } else {
                        line = absoluteUrl
                    }
                } catch (e: Exception) {
                    // Ignore resolve errors
                }
            }

            result.append(line).append("\n")
        }

        return result.toString()
    }

    private fun isAdSegment(url: String): Boolean {
        // 策略1：匹配广告正则
        if (AD_URL_PATTERN.matcher(url).find()) {
            return true
        }
        // TODO: 可在此处扩充策略2（突变时长检测）和策略3（Discontinuity扫描）
        return false
    }
}
