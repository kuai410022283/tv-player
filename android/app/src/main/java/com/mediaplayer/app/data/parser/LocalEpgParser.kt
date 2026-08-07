package com.mediaplayer.app.data.parser

import android.util.Xml
import com.mediaplayer.app.data.model.Channel
import org.xmlpull.v1.XmlPullParser
import java.io.InputStream
import java.io.InputStreamReader
import java.text.SimpleDateFormat
import java.util.Calendar
import java.util.Date
import java.util.Locale
import java.util.zip.GZIPInputStream

/**
 * 电子节目单 (EPG) 轻量 XML 流式解析器
 * 使用 XmlPullParser 逐行解析，严格限制内存以防 OOM，自动识别 GZip 格式
 */
object LocalEpgParser {

    data class ProgramInfo(
        val title: String,
        val startTime: Long,
        val endTime: Long
    )

    /**
     * 解析 EPG 并与频道列表建立绑定
     * @param inputStream 输入流
     * @param channels 需要绑定节目单的本地频道列表
     * @param isGzip 是否为 GZip 压缩格式
     */
    suspend fun parseAndBind(inputStream: InputStream, channels: List<Channel>, isGzip: Boolean, onBindComplete: (() -> Unit)? = null) {
        val stream = if (isGzip) GZIPInputStream(inputStream) else inputStream
        val parser = Xml.newPullParser()
        
        // 建立 tvg-id / 规范化频道名 -> 频道的映射以加快查找
        val idMap = mutableMapOf<String, Channel>()
        val nameMap = mutableMapOf<String, Channel>()
        
        channels.forEach { ch ->
            // 在 Parser.kt 中临时把 tvg-id 塞到了 description 中
            val tvgId = ch.description.trim().lowercase(Locale.ROOT)
            if (tvgId.isNotEmpty()) {
                idMap[tvgId] = ch
            }
            val normalizedName = normalizeChannelName(ch.name)
            nameMap[normalizedName] = ch
        }

        val sdf = SimpleDateFormat("yyyyMMddHHmmss Z", Locale.US)
        val nowMs = System.currentTimeMillis()

        // 今天、明天节目单的高效缓存
        // 频道ID -> 正在播出的节目 & 下一个节目
        val currentPrograms = mutableMapOf<Long, ProgramInfo>()
        val nextPrograms = mutableMapOf<Long, ProgramInfo>()

        try {
            parser.setInput(InputStreamReader(stream, "UTF-8"))
            var eventType = parser.eventType
            
            var currentChannelIdInTag: String? = null
            var startTime: Long = 0
            var endTime: Long = 0
            var title: String? = null
            var inProgramme = false

            while (eventType != XmlPullParser.END_DOCUMENT) {
                val tagName = parser.name
                when (eventType) {
                    XmlPullParser.START_TAG -> {
                        if (tagName == "programme") {
                            inProgramme = true
                            currentChannelIdInTag = parser.getAttributeValue(null, "channel")
                            val startAttr = parser.getAttributeValue(null, "start")
                            val endAttr = parser.getAttributeValue(null, "stop")
                            
                            startTime = parseTime(startAttr, sdf)
                            endTime = parseTime(endAttr, sdf)
                        } else if (tagName == "title" && inProgramme) {
                            title = parser.nextText()
                        }
                    }
                    XmlPullParser.END_TAG -> {
                        if (tagName == "programme") {
                            inProgramme = false
                            if (!currentChannelIdInTag.isNullOrEmpty() && title != null) {
                                // 查找目标频道
                                val targetChannel = idMap[currentChannelIdInTag.lowercase(Locale.ROOT)]
                                    ?: nameMap[normalizeChannelName(currentChannelIdInTag)]
                                
                                if (targetChannel != null) {
                                    // 仅解析当前时间段相关的节目单，防 OOM
                                    if (endTime > nowMs - 2 * 3600 * 1000 && startTime < nowMs + 24 * 3600 * 1000) {
                                        val prog = ProgramInfo(title, startTime, endTime)
                                        if (nowMs in startTime until endTime) {
                                            currentPrograms[targetChannel.id] = prog
                                        } else if (startTime >= endTime) {
                                            // 异常时间，跳过
                                        } else if (startTime in nowMs until (currentPrograms[targetChannel.id]?.endTime ?: (nowMs + 6 * 3600 * 1000))) {
                                            val existingNext = nextPrograms[targetChannel.id]
                                            if (existingNext == null || startTime < existingNext.startTime) {
                                                nextPrograms[targetChannel.id] = prog
                                            }
                                        }
                                    }
                                }
                            }
                            // 重置临时状态
                            currentChannelIdInTag = null
                            startTime = 0
                            endTime = 0
                            title = null
                        }
                    }
                }
                eventType = parser.next()
            }

            // 完成解析后，切换到主线程将数据安全地绑定回 Channel 模型中，规避并发修改脏读和 UI 崩溃
            kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.Main) {
                channels.forEach { ch ->
                    val current = currentPrograms[ch.id]
                    val next = nextPrograms[ch.id]
                    val timeFormat = SimpleDateFormat("HH:mm", Locale.getDefault())

                    if (current != null) {
                        val startStr = timeFormat.format(Date(current.startTime))
                        val endStr = timeFormat.format(Date(current.endTime))
                        ch.currentEpg = "$startStr-$endStr ${current.title}"
                        
                        val duration = current.endTime - current.startTime
                        if (duration > 0) {
                            val progress = ((nowMs - current.startTime).toFloat() / duration * 100).toInt()
                            ch.epgPercent = progress.coerceIn(0, 100)
                        }
                    } else {
                        ch.currentEpg = ""
                        ch.epgPercent = 0
                    }

                    if (next != null) {
                        val startStr = timeFormat.format(Date(next.startTime))
                        val endStr = timeFormat.format(Date(next.endTime))
                        ch.nextEpg = "$startStr-$endStr ${next.title}"
                    } else {
                        ch.nextEpg = ""
                    }
                }
                onBindComplete?.invoke()
            }
        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            try {
                stream.close()
            } catch (_: Exception) {}
        }
    }

    /**
     * 规范化频道名以便模糊匹配
     */
    private fun normalizeChannelName(name: String): String {
        return name.lowercase(Locale.ROOT)
            .replace(" ", "")
            .replace("-", "")
            .replace("hd", "")
            .replace("4k", "")
            .replace("高清", "")
            .replace("超清", "")
            .replace("综合", "")
    }

    /**
     * 解析时间戳
     */
    private fun parseTime(timeStr: String?, sdf: SimpleDateFormat): Long {
        if (timeStr.isNullOrEmpty()) return 0
        return try {
            // XMLTV 时间常用格式：20260807130000 +0800
            val cleaned = timeStr.trim()
            val date = sdf.parse(cleaned)
            date?.time ?: 0
        } catch (e: Exception) {
            // 降级处理：如果没有时区，尝试硬解析
            try {
                val basicSdf = SimpleDateFormat("yyyyMMddHHmmss", Locale.US)
                val date = basicSdf.parse(timeStr.trim().take(14))
                date?.time ?: 0
            } catch (_: Exception) {
                0
            }
        }
    }
}
