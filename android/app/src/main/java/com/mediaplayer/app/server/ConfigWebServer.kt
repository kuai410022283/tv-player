package com.mediaplayer.app.server

import android.content.Context
import fi.iki.elonen.NanoHTTPD

class ConfigWebServer(
    private val context: Context,
    port: Int = 9528,
    private val onUrlSaved: (String) -> Unit
) : NanoHTTPD(port) {

    override fun serve(session: IHTTPSession): Response {
        val method = session.method
        val uri = session.uri

        if (method == Method.GET && uri == "/") {
            return newFixedLengthResponse(getHtmlForm())
        }

        if (method == Method.POST && uri == "/save") {
            try {
                val map = HashMap<String, String>()
                session.parseBody(map)
                val params = session.parameters
                var newUrl = params["server_url"]?.firstOrNull()?.trim()
                val location = params["location"]?.firstOrNull()?.trim()

                if (!newUrl.isNullOrEmpty()) {
                    try {
                        val decodedBytes = android.util.Base64.decode(newUrl, android.util.Base64.DEFAULT)
                        val decodedStr = String(decodedBytes, Charsets.UTF_8).trim()
                        if (decodedStr.startsWith("http://", ignoreCase = true) || 
                            decodedStr.startsWith("https://", ignoreCase = true) ||
                            decodedStr.contains(".") || 
                            decodedStr.contains(":") ||
                            decodedStr.contains("localhost")) {
                            newUrl = decodedStr
                        }
                    } catch (e: Exception) {
                        // Not a valid Base64 string or failed to decode, keep the raw input
                    }

                    val prefs = context.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, Context.MODE_PRIVATE)
                    if (!location.isNullOrEmpty()) {
                        prefs.edit().putString("device_location", location).apply()
                    } else {
                        prefs.edit().remove("device_location").apply()
                    }

                    onUrlSaved(newUrl!!)
                    return newFixedLengthResponse(getSuccessHtml())
                } else {
                    return newFixedLengthResponse(Response.Status.BAD_REQUEST, MIME_HTML, "Invalid URL")
                }
            } catch (e: Exception) {
                e.printStackTrace()
                return newFixedLengthResponse(Response.Status.INTERNAL_ERROR, MIME_PLAINTEXT, "Error parsing body")
            }
        }

        return newFixedLengthResponse(Response.Status.NOT_FOUND, MIME_PLAINTEXT, "Not Found")
    }

    private fun getHtmlForm(): String {
        return """
            <!DOCTYPE html>
            <html lang="zh-CN">
            <head>
                <meta charset="UTF-8">
                <meta name="viewport" content="width=device-width, initial-scale=1.0">
                <title>服务器配置</title>
                <style>
                    body { font-family: -apple-system, sans-serif; background: #f4f4f5; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
                    .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); width: 90%; max-width: 400px; text-align: center; }
                    h2 { margin-top: 0; color: #333; }
                    input { width: 100%; padding: 12px; margin: 15px 0; border: 1px solid #ccc; border-radius: 6px; box-sizing: border-box; font-size: 16px; }
                    button { width: 100%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 6px; font-size: 16px; cursor: pointer; transition: 0.3s; }
                    button:hover { background: #0056b3; }
                </style>
            </head>
            <body>
                <div class="card">
                    <h2>欢迎使用</h2>
                    <p style="color:#666; font-size:14px;">请输入授权与位置信息</p>
                    <form action="/save" method="post">
                        <input type="text" name="server_url" placeholder="请在此处粘贴服务商提供的配置信息" required>
                        <input type="text" name="location" placeholder="安装位置（例如：客厅、主卧）" required>
                        <button type="submit">保存配置</button>
                    </form>
                </div>
            </body>
            </html>
        """.trimIndent()
    }

    private fun getSuccessHtml(): String {
        return """
            <!DOCTYPE html>
            <html lang="zh-CN">
            <head>
                <meta charset="UTF-8">
                <meta name="viewport" content="width=device-width, initial-scale=1.0">
                <title>配置成功</title>
                <style>
                    body { font-family: -apple-system, sans-serif; background: #f4f4f5; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
                    .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); width: 90%; max-width: 400px; text-align: center; }
                    h2 { color: #28a745; margin-top: 0; }
                    p { color: #666; }
                </style>
            </head>
            <body>
                <div class="card">
                    <h2>✅ 配置保存成功</h2>
                    <p>您的电视端已收到配置并正在重新连接服务器，您可以关闭此页面了。</p>
                </div>
            </body>
            </html>
        """.trimIndent()
    }
}
