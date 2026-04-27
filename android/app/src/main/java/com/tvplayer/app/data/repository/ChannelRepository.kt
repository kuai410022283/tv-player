package com.tvplayer.app.data.repository

import com.tvplayer.app.data.api.ApiClient
import com.tvplayer.app.data.model.*
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

class ChannelRepository {

    private val api = ApiClient.getApi()

    suspend fun getGroups(): Result<List<ChannelGroup>> = withContext(Dispatchers.IO) {
        try {
            val res = api.getGroups()
            if (res.isSuccessful && res.body()?.code == 0) {
                Result.success(res.body()!!.data ?: emptyList())
            } else {
                Result.failure(Exception(res.body()?.message ?: "加载失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun getChannels(
        groupId: Long? = null,
        favorite: Boolean? = null,
        search: String? = null
    ): Result<List<Channel>> = withContext(Dispatchers.IO) {
        try {
            val res = api.getChannels(groupId = groupId, favorite = favorite, search = search, pageSize = 500)
            if (res.isSuccessful && res.body()?.code == 0) {
                Result.success(res.body()!!.data?.items ?: emptyList())
            } else {
                Result.failure(Exception(res.body()?.message ?: "加载失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun getChannel(id: Long): Result<Channel> = withContext(Dispatchers.IO) {
        try {
            val res = api.getChannel(id)
            if (res.isSuccessful && res.body()?.code == 0 && res.body()!!.data != null) {
                Result.success(res.body()!!.data!!)
            } else {
                Result.failure(Exception("频道不存在"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun toggleFavorite(id: Long): Result<Unit> = withContext(Dispatchers.IO) {
        try {
            val res = api.toggleFavorite(id)
            if (res.isSuccessful) Result.success(Unit)
            else Result.failure(Exception("操作失败"))
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun addHistory(channelId: Long, duration: Int, lastPos: Int): Result<Unit> = withContext(Dispatchers.IO) {
        try {
            api.addHistory(PlayHistory(id = 0, channelId = channelId, duration = duration, lastPos = lastPos))
            Result.success(Unit)
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun getHistory(): Result<List<PlayHistory>> = withContext(Dispatchers.IO) {
        try {
            val res = api.getHistory()
            if (res.isSuccessful && res.body()?.code == 0) {
                Result.success(res.body()!!.data ?: emptyList())
            } else {
                Result.failure(Exception("加载失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }

    suspend fun getStats(): Result<ServerStats> = withContext(Dispatchers.IO) {
        try {
            val res = api.getStats()
            if (res.isSuccessful && res.body()?.code == 0) {
                Result.success(res.body()!!.data ?: ServerStats())
            } else {
                Result.failure(Exception("加载失败"))
            }
        } catch (e: Exception) {
            Result.failure(e)
        }
    }
}
