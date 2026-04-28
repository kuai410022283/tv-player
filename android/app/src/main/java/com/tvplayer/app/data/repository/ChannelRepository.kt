package com.tvplayer.app.data.repository

import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.model.Channel
import com.tvplayer.app.data.model.ChannelGroup
import com.tvplayer.app.data.model.EPGProgram
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * 频道数据仓库 —— 封装 API 调用，返回 Result。
 */
class ChannelRepository {

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

    /** 获取所有频道（自动分页拉取） */
    suspend fun getChannels(
        groupId: Long? = null,
        favorite: Boolean = false,
        search: String? = null
    ): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val allChannels = mutableListOf<Channel>()
            var page = 1
            val pageSize = 200

            while (true) {
                val resp = ApiClient.getService().getChannels(
                    groupId = groupId,
                    favorite = if (favorite) "true" else null,
                    search = search,
                    page = page,
                    pageSize = pageSize
                )

                if (resp.isSuccessful && resp.body()?.code == 0) {
                    val pageData = resp.body()!!.data
                    val items = pageData?.items ?: emptyList()
                    allChannels.addAll(items)

                    if (allChannels.size >= (pageData?.total ?: 0) || items.size < pageSize) {
                        break
                    }
                    page++
                } else {
                    return@withContext Result.failure(
                        Exception(resp.body()?.message ?: "获取频道失败")
                    )
                }
            }

            Result.success(allChannels)
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
