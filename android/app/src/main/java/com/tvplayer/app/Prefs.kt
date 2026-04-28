package com.tvplayer.app

/**
 * SharedPreferences 常量，统一管理偏好设置的文件名和键名。
 */
object Prefs {
    const val FILE = "tvplayer_prefs"

    // 通用设置
    const val KEY_SERVER_URL = "server_url"
    const val KEY_GESTURE_HINT_SHOWN = "gesture_hint_shown"

    // 认证信息
    const val KEY_ACCESS_TOKEN = "access_token"
    const val KEY_CLIENT_ID = "client_id"
    const val KEY_CLIENT_STATUS = "client_status"
    const val KEY_DEVICE_ID = "device_id"
}
