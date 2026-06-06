package com.mediaplayer.app

/**
 * SharedPreferences 常量，统一管理偏好设置的文件名和键名。
 */
object Prefs {
    const val FILE = "mediaplayer_prefs"

    // 通用设置
    const val DEFAULT_SERVER_URL = "http://0.0.0.0:9527"
    const val KEY_SERVER_URL = "server_url"
    const val KEY_GESTURE_HINT_SHOWN = "gesture_hint_shown"
    const val KEY_SHOW_CHANNEL_LOGO = "show_channel_logo"
    
    // 播放器设置
    const val KEY_NETWORK_CACHE = "network_cache_ms"
    const val DEFAULT_NETWORK_CACHE = 0
    
    // 解码模式：0=自动, 1=强制硬解, 2=强制软解
    const val KEY_DECODER_MODE = "decoder_mode"
    const val DECODER_MODE_AUTO = 0
    const val DECODER_MODE_HARDWARE = 1
    const val DECODER_MODE_SOFTWARE = 2
    
    // 播放内核
    const val KEY_PLAYER_CORE = "player_core"
    const val PLAYER_CORE_AUTO = 0
    const val PLAYER_CORE_VLC = 1
    const val PLAYER_CORE_EXO = 2
    const val PLAYER_CORE_IJK = 3
    
    // 开机自启
    const val KEY_AUTO_START = "auto_start"

    // 画面比例
    const val KEY_SCALE_MODE = "scale_mode"
    const val SCALE_MODE_DEFAULT = 0 // 自适应
    const val SCALE_MODE_STRETCH = 1 // 16:9 强行拉伸
    const val SCALE_MODE_CROP = 2    // 16:9 裁剪放大
    const val SCALE_MODE_4_3 = 3     // 强制 4:3

    // 认证信息
    const val KEY_ACCESS_TOKEN = "access_token"
    const val KEY_CLIENT_ID = "client_id"
    const val KEY_CLIENT_STATUS = "client_status"
    const val KEY_DEVICE_ID = "device_id"
    const val KEY_ENABLE_LOG = "enable_log"
}
