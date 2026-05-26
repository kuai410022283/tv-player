package com.mediaplayer.app.data.model

import com.google.gson.annotations.SerializedName

data class VerifyResponse(
    @SerializedName("client_id") val clientId: Long = 0,
    @SerializedName("name") val name: String = "",
    @SerializedName("announcement") val announcement: String? = null,
    @SerializedName("announcement_interval") val announcementInterval: Int = 0
)
