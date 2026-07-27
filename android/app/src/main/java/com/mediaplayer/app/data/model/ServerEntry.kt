package com.mediaplayer.app.data.model

/**
 * 服务器条目：包含名称和地址。
 * 用于存储服务器列表（支持本地、远程、备用服务器）。
 */
data class ServerEntry(
    val name: String = "",
    val url: String
)