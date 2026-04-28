package com.tvplayer.app.data.api

import com.tvplayer.app.data.model.*
import retrofit2.Response
import retrofit2.http.*

interface TVPlayerApi {

    // ── Client Auth ────────────────────────────────────
    @POST("client/register")
    suspend fun registerClient(@Body req: ClientRegisterReq): Response<ApiResponse<ClientRegisterResp>>

    @GET("client/verify")
    suspend fun verifyClient(@Header("Authorization") token: String): Response<ApiResponse<Map<String, Any>>>

    @GET("client/me")
    suspend fun getMyInfo(): Response<ApiResponse<Map<String, Any>>>

    // ── Groups ─────────────────────────────────────────
    @GET("groups")
    suspend fun getGroups(): Response<ApiResponse<List<ChannelGroup>>>

    // ── Channels ───────────────────────────────────────
    @GET("channels")
    suspend fun getChannels(
        @Query("group_id") groupId: Long? = null,
        @Query("favorite") favorite: Boolean? = null,
        @Query("search") search: String? = null,
        @Query("page") page: Int = 1,
        @Query("page_size") pageSize: Int = 500
    ): Response<ApiResponse<PageResponse<Channel>>>

    @GET("channels/{id}")
    suspend fun getChannel(@Path("id") id: Long): Response<ApiResponse<Channel>>

    @POST("channels/{id}/favorite")
    suspend fun toggleFavorite(@Path("id") id: Long): Response<ApiResponse<Unit>>

    // ── Stream ─────────────────────────────────────────
    @GET("stream/check/{id}")
    suspend fun checkStream(@Path("id") id: Long): Response<ApiResponse<StreamStatus>>

    // ── History ────────────────────────────────────────
    @GET("history")
    suspend fun getHistory(@Query("limit") limit: Int = 50): Response<ApiResponse<List<PlayHistory>>>

    @POST("history")
    suspend fun addHistory(@Body history: PlayHistory): Response<ApiResponse<PlayHistory>>

    // ── Settings ───────────────────────────────────────
    @GET("settings")
    suspend fun getSettings(): Response<ApiResponse<Map<String, String>>>

    @POST("settings")
    suspend fun setSetting(@Body setting: Map<String, String>): Response<ApiResponse<Unit>>

    // ── Stats ──────────────────────────────────────────
    @GET("stats")
    suspend fun getStats(): Response<ApiResponse<ServerStats>>

    // ── EPG ────────────────────────────────────────────
    @GET("epg")
    suspend fun getEPG(@Query("channel_id") channelId: String): Response<ApiResponse<List<EPGProgram>>>
}
