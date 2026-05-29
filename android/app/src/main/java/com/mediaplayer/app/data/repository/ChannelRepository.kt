package com.mediaplayer.app.data.repository

import com.mediaplayer.app.data.api.ApiClient
import com.mediaplayer.app.data.model.Channel
import com.mediaplayer.app.data.model.ChannelGroup
import com.mediaplayer.app.data.model.EPGProgram
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withContext

/**
 * 频道数据仓库 —— 封装 API 调用，返回 Result。
 */
class ChannelRepository {

    companion object {
        private const val PAGE_SIZE = 200 // 与后端 page_size 上限对齐
    }

    /** 获取所有分组 */
    suspend fun getGroups(): Result<List<ChannelGroup>> = withContext(Dispatchers.IO) {
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
     * 按分组并行拉取所有频道（解决全局 page_size 上限问题）。
     *
     * 利用 设备→套餐→{分组1, 分组2,...} 结构，对每个分组单独请求，
     * 每组频道数通常远小于单页上限，同时支持自动翻页以应对超大分组。
     *
     * @param groups 已获取的分组列表（不含"全部"虚拟分组 id=0）
     */
    suspend fun getAllChannelsByGroups(
        groups: List<ChannelGroup>
    ): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val allItems = coroutineScope {
                groups.map { group ->
                    async { fetchAllChannelsForGroup(group.id) }
                }.awaitAll()
            }.flatten()
            Result.success(allItems)
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    /**
     * 拉取单个分组的全量频道，自动翻页直到取完该分组所有数据。
     */
    private suspend fun fetchAllChannelsForGroup(groupId: Long): List<Channel> {
        val result = mutableListOf<Channel>()
        var page = 1
        while (true) {
            val resp = try {
                ApiClient.getService().getChannels(
                    groupId = groupId,
                    page = page,
                    pageSize = PAGE_SIZE
                )
            } catch (e: Exception) {
                break
            }
            if (!resp.isSuccessful || resp.body()?.code != 0) break
            val pageData = resp.body()!!.data ?: break
            result.addAll(pageData.items)
            // 已取完该分组所有数据则退出
            if (result.size >= pageData.total) break
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
