package com.tvplayer.app

/**
 * SharedPreferences 常量，统一管理偏好设置的文件名和键名。
 */
object Prefs {
    const val FILE = "tvplayer_prefs"

    // 通用设置
    const val DEFAULT_SERVER_URL = "http://nas.laokhome.cn:9527"
    const val KEY_SERVER_URL = "server_url"
    const val KEY_GESTURE_HINT_SHOWN = "gesture_hint_shown"
    
    // 播放器设置
    const val KEY_NETWORK_CACHE = "network_cache_ms"
    const val DEFAULT_NETWORK_CACHE = 1500
    
    // 解码模式：0=自动, 1=强制硬解, 2=强制软解
    const val KEY_DECODER_MODE = "decoder_mode"
    const val DECODER_MODE_AUTO = 0
    const val DECODER_MODE_HARDWARE = 1
    const val DECODER_MODE_SOFTWARE = 2

    // 认证信息
    const val KEY_ACCESS_TOKEN = "access_token"
    const val KEY_CLIENT_ID = "client_id"
    const val KEY_CLIENT_STATUS = "client_status"
    const val KEY_DEVICE_ID = "device_id"
}
