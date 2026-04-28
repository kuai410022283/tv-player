package com.tvplayer.app.data.model

import com.google.gson.annotations.SerializedName

data class APIResponse<T>(
    @SerializedName("code") val code: Int = 0,
    @SerializedName("message") val message: String = "",
    @SerializedName("data") val data: T? = null
)

data class PageResponse<T>(
    @SerializedName("total") val total: Long = 0,
    @SerializedName("page") val page: Int = 1,
    @SerializedName("page_size") val pageSize: Int = 20,
    @SerializedName("items") val items: List<T> = emptyList()
)

data class ClientRegisterResp(
    @SerializedName("client_id") val clientId: Long = 0,
    @SerializedName("status") val status: String = "",
    @SerializedName("access_token") val accessToken: String = "",
    @SerializedName("expires_at") val expiresAt: String = "",
    @SerializedName("message") val message: String = ""
)
