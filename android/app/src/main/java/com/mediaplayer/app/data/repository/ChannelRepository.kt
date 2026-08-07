package com.mediaplayer.app.data.repository

import com.mediaplayer.app.data.api.ApiClient
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelGroup
import com.mediaplayer.app.data.model.EPGProgram
import com.mediaplayer.app.data.model.PageResponse
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.channelFlow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext

/**
 * 频道数据仓库 —— 封装 API 调用，返回 Result。
 */
class ChannelRepository {

    companion object {
        private const val PAGE_SIZE = 500 // 恢复为 500 条/页
    }

    // 本地数据缓存
    private var localCachedData: List<Pair<ChannelGroup, List<Channel>>>? = null

    /** 获取所有分组 */
    suspend fun getGroups(context: android.content.Context): Result<List<ChannelGroup>> = withContext(Dispatchers.IO) {
        val prefs = context.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, android.content.Context.MODE_PRIVATE)
        val localEnabled = prefs.getBoolean(com.mediaplayer.app.Prefs.KEY_LOCAL_SOURCE_ENABLED, false)
        val playlistUrl = prefs.getString(com.mediaplayer.app.Prefs.KEY_LOCAL_PLAYLIST_URL, "") ?: ""

        if (localEnabled && playlistUrl.isNotEmpty()) {
            try {
                // 如果没有缓存，则发起网络请求下载并解析
                val content = if (playlistUrl.startsWith("http://") || playlistUrl.startsWith("https://")) {
                    val call = okhttp3.OkHttpClient().newCall(okhttp3.Request.Builder().url(playlistUrl).build())
                    call.execute().body?.string() ?: ""
                } else {
                    ""
                }
                
                if (content.isNotEmpty()) {
                    var detectedEpgUrl = ""
                    val parsed = com.mediaplayer.app.data.parser.LocalPlaylistParser.parse(content) { epg ->
                        detectedEpgUrl = epg
                    }
                    if (parsed.isNotEmpty()) {
                        localCachedData = parsed
                        
                        // 异步拉取并解析 EPG 绑定
                        var epgUrl = prefs.getString(com.mediaplayer.app.Prefs.KEY_LOCAL_EPG_URL, "") ?: ""
                        if (epgUrl.isEmpty() && detectedEpgUrl.isNotEmpty()) {
                            // 自动回填检测到的 M3U 头部 EPG 地址
                            epgUrl = detectedEpgUrl
                            prefs.edit().putString(com.mediaplayer.app.Prefs.KEY_LOCAL_EPG_URL, epgUrl).apply()
                        }
                        if (epgUrl.isNotEmpty()) {
                            kotlinx.coroutines.GlobalScope.launch(Dispatchers.IO) {
                                try {
                                    val client = okhttp3.OkHttpClient.Builder()
                                        .connectTimeout(5, java.util.concurrent.TimeUnit.SECONDS)
                                        .readTimeout(10, java.util.concurrent.TimeUnit.SECONDS)
                                        .build()
                                    val req = okhttp3.Request.Builder().url(epgUrl).build()
                                    val res = client.newCall(req).execute()
                                    res.body?.byteStream()?.let { stream ->
                                        val isGzip = epgUrl.endsWith(".gz") || (res.header("Content-Encoding")?.contains("gzip") ?: false)
                                        val allChannels = parsed.flatMap { it.second }
                                        com.mediaplayer.app.data.parser.LocalEpgParser.parseAndBind(stream, allChannels, isGzip) {
                                            // 绑定成功后，通知 UI 刷新列表
                                            (context as? android.app.Activity)?.runOnUiThread {
                                                try {
                                                    // 利用反射或直接调用通知 UI 刷新 RecyclerView
                                                    val rv = (context as? android.app.Activity)?.findViewById<androidx.recyclerview.widget.RecyclerView>(com.mediaplayer.app.R.id.rvChannels)
                                                    rv?.adapter?.notifyDataSetChanged()
                                                } catch (_: Exception) {}
                                            }
                                        }
                                    }
                                } catch (e: Exception) {
                                    e.printStackTrace()
                                }
                            }
                        }
                        
                        return@withContext Result.success(parsed.map { it.first })
                    }
                }
            } catch (e: Exception) {
                e.printStackTrace()
                // 降级为网络获取
            }
        }

        // 云端获取逻辑
        try {
            val resp = ApiClient.getService().getGroups()
            if (resp.isSuccessful && resp.body()?.code == 0) {
                Result.success(resp.body()!!.data ?: emptyList())
            } else {
                Result.failure(Exception(resp.body()?.message ?: "获取分组失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 懒加载频道：通过首组特权优先拉取，解决定位延迟。
     * 后台并发增量加载其余组数据。
     */
    fun loadChannelsLazy(
        groups: List<ChannelGroup>
    ): Flow<List<Channel>> = channelFlow {
        if (groups.isEmpty()) return@channelFlow

        // 如果是本地数据源，直接吐出缓存的频道数据
        val localData = localCachedData
        if (localData != null) {
            localData.forEach { (_, channels) ->
                send(channels)
            }
            return@channelFlow
        }

        val semaphore = Semaphore(20)

        // 1. 首组绝对优先通道（VIP通道）
        // groups 的第 0 个已经被 MainActivity 强行提权为上次观看的组
        val firstGroup = groups.first()
        val firstPageResp = fetchChannelsPage(page = 1, pageSize = PAGE_SIZE, groupId = firstGroup.id)
        val firstPageChannels = firstPageResp.items ?: emptyList()
        if (firstPageChannels.isNotEmpty()) {
            send(firstPageChannels) // 第一时间送达前端 UI，秒切秒播
            
            // 如果这个首组数据量极大（超过 500），在后台默默补齐
            if (firstPageChannels.size < firstPageResp.total) {
                launch {
                    semaphore.withPermit {
                        fetchRemainingForGroup(firstGroup.id, 2, firstPageChannels.size, firstPageResp.total, this@channelFlow)
                    }
                }
            }
        }

        // 2. 剩余分组进入高并发排队系统
        val otherGroups = groups.drop(1)
        otherGroups.forEach { group ->
            launch {
                semaphore.withPermit {
                    val resp = fetchChannelsPage(page = 1, pageSize = PAGE_SIZE, groupId = group.id)
                    val channels = resp.items ?: emptyList()
                    if (channels.isNotEmpty()) {
                        send(channels) // 只要拿到任何一组的首页，立刻增量合并给前端
                        
                        if (channels.size < resp.total) {
                            fetchRemainingForGroup(group.id, 2, channels.size, resp.total, this@channelFlow)
                        }
                    }
                }
            }
        }
    }.flowOn(Dispatchers.IO)

    private suspend fun fetchRemainingForGroup(
        groupId: Long,
        startPage: Int,
        initialFetchedCount: Int,
        total: Long,
        flowScope: kotlinx.coroutines.channels.ProducerScope<List<Channel>>
    ) {
        var page = startPage
        var totalFetched = initialFetchedCount
        while (totalFetched < total) {
            val resp = fetchChannelsPage(page, PAGE_SIZE, groupId)
            val items = resp.items ?: emptyList()
            if (items.isEmpty()) break
            
            flowScope.send(items)
            totalFetched += items.size
            if (items.size < PAGE_SIZE) break
            page++
        }
    }

    private suspend fun fetchChannelsPage(page: Int, pageSize: Int, groupId: Long? = null): PageResponse<Channel> {
        return try {
            val resp = ApiClient.getService().getChannels(
                groupId = groupId,
                page = page,
                pageSize = pageSize
            )
            if (resp.isSuccessful && resp.body()?.code == 0) {
                resp.body()!!.data ?: PageResponse()
            } else {
                PageResponse()
            }
        } catch (e: Exception) {
            PageResponse()
        }
    }

    /**
     * 按分组并发拉取所有频道，同样利用 Semaphore(20) 限制并发，杜绝服务端 530 错误。
     */
    suspend fun getAllChannelsByGroups(
        groups: List<ChannelGroup>
    ): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val semaphore = Semaphore(20)
            val allItems = coroutineScope {
                groups.map { group ->
                    async {
                        semaphore.withPermit {
                            fetchAllChannelsForGroup(group.id)
                        }
                    }
                }.awaitAll()
            }.flatten()
            Result.success(allItems)
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    private suspend fun fetchAllChannelsForGroup(groupId: Long): List<Channel> {
        val result = mutableListOf<Channel>()
        var page = 1
        while (true) {
            val resp = fetchChannelsPage(page, PAGE_SIZE, groupId)
            val fetchedItems = resp.items ?: emptyList()
            if (fetchedItems.isEmpty()) break
            
            result.addAll(fetchedItems)
            if (fetchedItems.size < PAGE_SIZE || result.size >= resp.total) break
            page++
        }
        return result
    }

    /**
     * 搜索频道（用于关键词搜索场景，不分组，单页返回）。
     */
    suspend fun searchChannels(search: String): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val resp = ApiClient.getService().getChannels(
                search = search,
                page = 1,
                pageSize = PAGE_SIZE
            )
            if (resp.isSuccessful && resp.body()?.code == 0) {
                Result.success(resp.body()!!.data?.items ?: emptyList())
            } else {
                Result.failure(Exception(resp.body()?.message ?: "搜索频道失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /** 获取 EPG 节目单 */
    suspend fun getEPG(channelId: String): Result<List<EPGProgram>> = withContext(Dispatchers.IO) {
        try {
            val resp = ApiClient.getService().getEPG(channelId)
            if (resp.isSuccessful && resp.body()?.code == 0) {
                Result.success(resp.body()!!.data ?: emptyList())
            } else {
                Result.success(emptyList())
            }
        } catch (e: Exception) {
            Result.success(emptyList())
        }
    }

    /** 记录播放历史 */
    suspend fun addHistory(
        channelId: Long,
        duration: Int,
        lastPos: Int,
        clientId: Long
    ): Result<Unit> = withContext(Dispatchers.IO) {
        try {
            val body = mapOf(
                "channel_id" to channelId,
                "duration" to duration,
                "last_pos" to lastPos,
                "client_id" to clientId
            )
            ApiClient.getService().addHistory(body)
            Result.success(Unit)
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
}
