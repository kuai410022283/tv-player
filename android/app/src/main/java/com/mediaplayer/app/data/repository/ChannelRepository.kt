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
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.flowOn
import kotlinx.coroutines.withContext

/**
 * 频道数据仓库 —— 封装 API 调用，返回 Result。
 */
class ChannelRepository {

    companion object {
        private const val PAGE_SIZE = 2000 // 每页大小
        private const val FIRST_PAGE_SIZE = 2000 // 首页快速加载大小
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
     * 懒加载频道：先返回首页数据用于快速显示，后台继续加载剩余数据。
     *
     * @param groups 已获取的分组列表（不含"全部"虚拟分组 id=0）
     * @return Flow，第一项是首页数据（快速显示），后续项是增量数据（后台加载）
     */
    fun loadChannelsLazy(
        groups: List<ChannelGroup>
    ): Flow<List<Channel>> = flow {
        val groupIds = groups.map { it.id }.toSet()

        // 1. 获取第一页（使用较大的 PageSize，快速显示大部分/所有频道）
        val firstPageResult = fetchChannelsPage(page = 1, pageSize = FIRST_PAGE_SIZE)
        val firstPageChannels = firstPageResult.items?.filter { groupIds.contains(it.groupId) } ?: emptyList()
        emit(firstPageChannels)

        // 2. 如果还有更多数据，后台继续加载
        val total = firstPageResult.total
        val loadedSize = firstPageResult.items?.size ?: 0
        if (loadedSize > 0 && loadedSize < total) {
            val remainingChannels = fetchRemainingChannelsGlobal(
                startPage = 2,
                initialFetchedCount = loadedSize,
                groupIds = groupIds
            )
            if (remainingChannels.isNotEmpty()) {
                emit(remainingChannels)
            }
        }
    }.flowOn(Dispatchers.IO)

    private suspend fun fetchChannelsPage(page: Int, pageSize: Int): PageResponse<Channel> {
        return try {
            val resp = ApiClient.getService().getChannels(
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

    private suspend fun fetchRemainingChannelsGlobal(
        startPage: Int,
        initialFetchedCount: Int,
        groupIds: Set<Long>
    ): List<Channel> {
        val result = mutableListOf<Channel>()
        var page = startPage
        var totalFetched = initialFetchedCount
        while (true) {
            val pageData = fetchChannelsPage(page, PAGE_SIZE)
            val fetchedItems = pageData.items ?: emptyList()
            if (fetchedItems.isEmpty()) break

            totalFetched += fetchedItems.size
            val filtered = fetchedItems.filter { groupIds.contains(it.groupId) }
            result.addAll(filtered)

            if (fetchedItems.size < PAGE_SIZE || totalFetched >= pageData.total) break
            page++
        }
        return result
    }

    /**
     * 按分组并行拉取所有频道（现已优化为全局单流拉取，解决分组并行请求过多导致服务器负载过大和530错误的问题）。
     *
     * @param groups 已获取的分组列表（不含"全部"虚拟分组 id=0）
     */
    suspend fun getAllChannelsByGroups(
        groups: List<ChannelGroup>
    ): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val groupIds = groups.map { it.id }.toSet()
            val result = mutableListOf<Channel>()
            var page = 1
            var totalFetched = 0
            while (true) {
                val pageData = fetchChannelsPage(page, PAGE_SIZE)
                val fetchedItems = pageData.items ?: emptyList()
                if (fetchedItems.isEmpty()) break

                totalFetched += fetchedItems.size
                val filtered = fetchedItems.filter { groupIds.contains(it.groupId) }
                result.addAll(filtered)

                if (fetchedItems.size < PAGE_SIZE || totalFetched >= pageData.total) break
                page++
            }
            Result.success(result)
        } catch (e: Exception) {
            Result.failure(e)
        }
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
