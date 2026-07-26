package com.mediaplayer.app.server

import android.content.Context
import android.content.Intent
import fi.iki.elonen.NanoHTTPD

/**
 * 专门用于接收局域网控制指令（如投屏切台）的轻量级 Web 服务器。
 * 使用随机可用端口，避免与 ConfigWebServer (端口 9528) 冲突。
 */
class ControlWebServer(private val context: Context) : NanoHTTPD(0) {

    companion object {
        const val ACTION_CONTROL_PLAY = "com.mediaplayer.app.ACTION_CONTROL_PLAY"
        const val EXTRA_CHANNEL_ID = "extra_channel_id"
        const val EXTRA_POSITION = "extra_position"
    }

    override fun serve(session: IHTTPSession): Response {
        val method = session.method
        val uri = session.uri

        // 接收局域网内的遥控切台指令
        if (method == Method.POST && uri == "/control/play") {
            try {
                val map = HashMap<String, String>()
                session.parseBody(map)
                
                // For application/json, NanoHTTPD puts the raw body or temp file path in "postData"
                var bodyText = map["postData"] ?: ""
                
                // If it looks like a file path, read it
                if (bodyText.startsWith("/") && java.io.File(bodyText).exists()) {
                    bodyText = java.io.File(bodyText).readText()
                }

                if (bodyText.isNotBlank()) {
                    try {
                        val json = org.json.JSONObject(bodyText)
                        val channelId = json.optLong("channel_id", -1L)
                        val position = json.optLong("position", 0L)
                        if (channelId != -1L) {
                            val intent = Intent(ACTION_CONTROL_PLAY)
                            intent.putExtra(EXTRA_CHANNEL_ID, channelId)
                            intent.putExtra(EXTRA_POSITION, position)
                            intent.setPackage(context.packageName)
                            context.sendBroadcast(intent)
                            
                            return newFixedLengthResponse(Response.Status.OK, "application/json", """{"status":"ok"}""")
                        }
                    } catch (e: Exception) {
                        e.printStackTrace()
                    }
                }
                return newFixedLengthResponse(Response.Status.BAD_REQUEST, "application/json", """{"status":"error", "message":"invalid payload"}""")
            } catch (e: Exception) {
                e.printStackTrace()
                return newFixedLengthResponse(Response.Status.INTERNAL_ERROR, "application/json", """{"status":"error"}""")
            }
        }

        return newFixedLengthResponse(Response.Status.NOT_FOUND, MIME_PLAINTEXT, "Not Found")
    }
}
