package com.mediaplayer.app.data.parser

import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelGroup
import com.mediaplayer.app.data.model.ChannelLine
import java.io.BufferedReader
import java.io.StringReader
import java.util.Locale
import kotlin.math.abs

/**
 * 本地播放列表解析器
 * 支持 M3U / M3U8 及 TXT 分组格式
 */
object LocalPlaylistParser {

    /**
     * 判断播放列表格式并解析为统一的分组和频道结构
     */
    fun parse(content: String, onEpgFound: ((String) -> Unit)? = null): List<Pair<ChannelGroup, List<Channel>>> {
        return if (content.contains("#EXTM3U", ignoreCase = true)) {
            parseM3u(content, onEpgFound)
        } else {
            parseTxt(content)
        }
    }

    /**
     * 根据 URL 后缀或特征智能推断流媒体协议类型，匹配云端 API 规范，避免显示 CUSTOM 徽标
     */
    private fun inferStreamType(url: String): String {
        val lowerUrl = url.lowercase(Locale.ROOT)
        return when {
            lowerUrl.contains(".m3u8") || lowerUrl.contains(".m3u") -> "hls"
            lowerUrl.contains(".ts") || lowerUrl.contains("/play.ts") || lowerUrl.contains("play.ts") -> "ts"
            lowerUrl.startsWith("rtsp://") -> "rtsp"
            lowerUrl.startsWith("rtmp://") -> "rtmp"
            lowerUrl.contains(".flv") -> "flv"
            lowerUrl.contains(".mp4") || lowerUrl.contains(".mkv") || lowerUrl.contains(".mov") || lowerUrl.contains(".avi") -> "vod"
            else -> "auto" // 降级为 AUTO，由播放器解复用器自适应探测，拒绝显示 CUSTOM
        }
    }

    /**
     * 解析 TXT 格式数据
     * 格式示例：
     * 央视频道,#genre#
     * CCTV-1,http://line1
     * CCTV-2,http://line1#http://line2
     */
    private fun parseTxt(content: String): List<Pair<ChannelGroup, List<Channel>>> {
        val result = mutableListOf<Pair<ChannelGroup, MutableList<Channel>>>()
        var currentGroupChannels = mutableListOf<Channel>()
        var currentGroup = ChannelGroup(id = 100000L, name = "默认分组", sortOrder = 0)

        // 默认先加入一个默认分组
        result.add(Pair(currentGroup, currentGroupChannels))

        val reader = BufferedReader(StringReader(content))
        var line: String?
        var channelIndex = 200000L

        try {
            while (reader.readLine().also { line = it } != null) {
                val trimmed = line?.trim() ?: continue
                if (trimmed.isEmpty() || trimmed.startsWith("#")) continue

                val parts = trimmed.split(",", limit = 2)
                if (parts.size < 2) continue

                val name = parts[0].trim()
                val value = parts[1].trim()

                if (value.equals("#genre#", ignoreCase = true)) {
                    // 遇到新分组
                    val groupId = 100000L + abs(name.hashCode().toLong()) % 50000L
                    val newGroup = ChannelGroup(id = groupId, name = name, sortOrder = result.size)
                    currentGroupChannels = mutableListOf()
                    currentGroup = newGroup
                    result.add(Pair(currentGroup, currentGroupChannels))
                } else if (value.contains("://")) {
                    // 播放源行，支持多线路以 # 分割
                    val urls = value.split("#")
                    val linesList = urls.mapIndexed { idx, url ->
                        ChannelLine(
                            id = channelIndex + idx,
                            streamUrl = url.trim(),
                            streamType = inferStreamType(url.trim()),
                            contentType = ""
                        )
                    }

                    // 检查是否在当前分组内有同名频道进行合并
                    val existingChannel = currentGroupChannels.find { it.name.equals(name, ignoreCase = true) }
                    if (existingChannel != null) {
                        val mergedLines = existingChannel.lines.toMutableList()
                        linesList.forEach { mergedLines.add(it) }
                        val updatedChannel = existingChannel.copy(lines = mergedLines)
                        val idx = currentGroupChannels.indexOf(existingChannel)
                        currentGroupChannels[idx] = updatedChannel
                    } else {
                        val channelId = 200000L + abs(name.hashCode().toLong()) % 100000L
                        val channel = Channel(
                            id = channelId,
                            groupId = currentGroup.id,
                            name = name,
                            logo = "",
                            sortOrder = currentGroupChannels.size,
                            isDirect = true,
                            lines = linesList
                        )
                        currentGroupChannels.add(channel)
                        channelIndex += linesList.size + 1
                    }
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            reader.close()
        }

        // 过滤空分组并返回
        return result.filter { it.second.isNotEmpty() }
    }

    /**
     * 解析 M3U / M3U8 格式数据
     */
    private fun parseM3u(content: String, onEpgFound: ((String) -> Unit)? = null): List<Pair<ChannelGroup, List<Channel>>> {
        val groupMap = mutableMapOf<String, ChannelGroup>()
        val channelMap = mutableMapOf<String, Pair<ChannelGroup, MutableList<Channel>>>()

        val reader = BufferedReader(StringReader(content))
        var line: String?
        
        var tempGroupTitle = "其他频道"
        var tempLogo = ""
        var tempTvgId = ""
        var tempTvgName = ""
        var tempName = ""

        val groupPattern = Regex("""group-title="([^"]+)"""", RegexOption.IGNORE_CASE)
        val logoPattern = Regex("""tvg-logo="([^"]+)"""", RegexOption.IGNORE_CASE)
        val idPattern = Regex("""tvg-id="([^"]+)"""", RegexOption.IGNORE_CASE)
        val namePattern = Regex("""tvg-name="([^"]+)"""", RegexOption.IGNORE_CASE)
        
        // 匹配 x-tvg-url="url" 或 url-tvg="url" 属性的正则
        val epgUrlPattern = Regex("""(?:x-tvg-url|url-tvg)="([^"]+)"""", RegexOption.IGNORE_CASE)

        var channelIndex = 300000L

        try {
            while (reader.readLine().also { line = it } != null) {
                val trimmed = line?.trim() ?: continue
                if (trimmed.isEmpty()) continue

                if (trimmed.startsWith("#EXTM3U", ignoreCase = true)) {
                    val match = epgUrlPattern.find(trimmed)
                    val tvgUrls = match?.groupValues?.get(1)?.trim() ?: ""
                    if (tvgUrls.isNotEmpty()) {
                        // 取逗号分隔的第一个 EPG 链接
                        val firstUrl = tvgUrls.split(",").firstOrNull()?.trim() ?: ""
                        if (firstUrl.isNotEmpty()) {
                            onEpgFound?.invoke(firstUrl)
                        }
                    }
                    continue
                }

                if (trimmed.startsWith("#EXTINF:")) {
                    // 解析 EXTINF 属性
                    val groupMatch = groupPattern.find(trimmed)
                    val logoMatch = logoPattern.find(trimmed)
                    val idMatch = idPattern.find(trimmed)
                    val nameMatch = namePattern.find(trimmed)

                    tempGroupTitle = groupMatch?.groupValues?.get(1)?.trim() ?: "其他频道"
                    tempLogo = logoMatch?.groupValues?.get(1)?.trim() ?: ""
                    tempTvgId = idMatch?.groupValues?.get(1)?.trim() ?: ""
                    tempTvgName = nameMatch?.groupValues?.get(1)?.trim() ?: ""

                    // 提取频道名称（逗号后面的内容）
                    val commaIndex = trimmed.lastIndexOf(',')
                    tempName = if (commaIndex != -1 && commaIndex < trimmed.length - 1) {
                        trimmed.substring(commaIndex + 1).trim()
                    } else {
                        ""
                    }
                } else if (!trimmed.startsWith("#") && trimmed.contains("://")) {
                    if (tempName.isEmpty()) {
                        tempName = "未命名频道"
                    }

                    // 创建或获取分组
                    val group = groupMap.getOrPut(tempGroupTitle) {
                        val groupId = 100000L + abs(tempGroupTitle.hashCode().toLong()) % 50000L
                        ChannelGroup(id = groupId, name = tempGroupTitle, sortOrder = groupMap.size)
                    }

                    val key = "${group.name}_${tempName}"
                    val streamType = inferStreamType(trimmed)
                    
                    val lineItem = ChannelLine(
                        id = channelIndex++,
                        streamUrl = trimmed,
                        streamType = streamType,
                        contentType = ""
                    )

                    val existingPair = channelMap[key]
                    if (existingPair != null) {
                        val ch = existingPair.second.first()
                        existingPair.second[0] = ch.copy(lines = ch.lines + lineItem)
                    } else {
                        val channelId = 200000L + abs(tempName.hashCode().toLong()) % 100000L
                        val list = mutableListOf(lineItem)
                        // 利用 description 字段临时存储 tvgId，以在 Epg 适配器中匹配使用
                        val ch = Channel(
                            id = channelId,
                            groupId = group.id,
                            name = tempName,
                            logo = tempLogo,
                            description = tempTvgId.ifEmpty { tempTvgName },
                            sortOrder = 0,
                            isDirect = true,
                            lines = list
                        )
                        channelMap[key] = Pair(group, mutableListOf(ch))
                    }

                    // 清空临时状态
                    tempName = ""
                    tempLogo = ""
                    tempTvgId = ""
                    tempTvgName = ""
                }
            }
        } catch (e: Exception) {
            e.printStackTrace()
        } finally {
            reader.close()
        }

        // 按分组重组结构
        val finalGroups = mutableMapOf<Long, Pair<ChannelGroup, MutableList<Channel>>>()
        channelMap.values.forEach { (group, channels) ->
            val pair = finalGroups.getOrPut(group.id) { Pair(group, mutableListOf()) }
            pair.second.addAll(channels)
        }

        // 返回
        return finalGroups.values.map { (group, list) ->
            Pair(group, list)
        }
    }
}
