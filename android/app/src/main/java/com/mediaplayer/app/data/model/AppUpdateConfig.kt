package com.mediaplayer.app.data.model

data class AppUpdateConfig(
    val version_code: Int,
    val version_name: String?,
    val download_url: String?,
    val update_log: String?,
    val force_update: Boolean
)
