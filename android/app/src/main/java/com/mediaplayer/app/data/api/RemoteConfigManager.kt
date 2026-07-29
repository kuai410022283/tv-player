package com.mediaplayer.app.data.api

import android.content.Context
import android.content.SharedPreferences
import com.google.gson.Gson
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.data.model.RemoteClientConfig

/**
 * 远程配置管理器：将后端下发的 RemoteClientConfig 应用到本地 SharedPreferences。
 *
 * 设计原则：
 * - 字段为 null → 不覆盖本地设置（不管控语义）
 * - 字段非 null → 强制覆盖本地设置
 * - 每次 verify 心跳成功后调用，保证配置持续有效
 */
object RemoteConfigManager {

    // 已被远程管控的 Prefs key 集合（用于设置界面显示锁定状态）
    private val managedKeys = mutableSetOf<String>()

    // 已被远程配置为隐藏的 Prefs key 集合（客户端UI中不显示）
    private val hiddenKeys = mutableSetOf<String>()

    // 面板隐藏标志（禁整面板）
    private var hideSettingsPanel: Boolean = false
    private var hideChannelList: Boolean = false
    private var hideEpgPanel: Boolean = false
    private var hideOsdPanel: Boolean = false

    /** 远程配置应用后的回调，通知 UI 重新读取缓存设置 */
    var onConfigApplied: (() -> Unit)? = null

    /**
     * 将远程配置应用到 SharedPreferences。
     * 仅对非 null 字段执行写入，null 值完全跳过。
     */
    fun applyConfig(context: Context, config: RemoteClientConfig) {
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        managedKeys.clear()
        hiddenKeys.clear()
        prefs.edit().apply {
            applyInt(this, prefs, Prefs.KEY_PLAYER_CORE, config.playerCore)
            applyInt(this, prefs, Prefs.KEY_DECODER_MODE, config.decoderMode)
            applyInt(this, prefs, Prefs.KEY_NETWORK_CACHE, config.networkCacheMs)
            applyBool(this, prefs, Prefs.KEY_AUDIO_PASSTHROUGH, config.audioPassthrough)
            applyInt(this, prefs, Prefs.KEY_SCALE_MODE, config.scaleMode)
            applyInt(this, prefs, Prefs.KEY_DNS_POLICY, config.dnsPolicy)
            applyBool(this, prefs, Prefs.KEY_STOP_PREVIOUS_MEDIA, config.stopPreviousMedia)
            applyBool(this, prefs, Prefs.KEY_SHOW_CHANNEL_LOGO, config.showChannelLogo)
            applyBool(this, prefs, Prefs.KEY_SHOW_GROUP_SOURCE, config.showGroupSource)
            applyInt(this, prefs, Prefs.KEY_GLOBAL_PROGRESS_BAR, config.globalProgressBar)
            applyInt(this, prefs, Prefs.KEY_TIME_SHOW_MODE, config.timeShowMode)
            applyInt(this, prefs, Prefs.KEY_CONTROL_SCHEME, config.controlScheme)
            applyBool(this, prefs, Prefs.KEY_AUTO_START, config.autoStart)
            applyBool(this, prefs, Prefs.KEY_ENABLE_PIP, config.enablePip)
            applyBool(this, prefs, Prefs.KEY_LOCAL_PROXY_ENABLED, config.localProxyEnabled)
            applyBool(this, prefs, Prefs.KEY_GESTURE_BRIGHTNESS, config.gestureBrightness)
            applyBool(this, prefs, Prefs.KEY_GESTURE_VOLUME, config.gestureVolume)
            applyBool(this, prefs, Prefs.KEY_REVERSE_CHANNEL_KEYS, config.reverseChannelKeys)
            applyBool(this, prefs, Prefs.KEY_AUTO_CHECK_UPDATE, config.autoCheckUpdate)
            applyInt(this, prefs, Prefs.KEY_PREFERRED_SERVER_INDEX, config.preferredServerIndex)
            applyInt(this, prefs, Prefs.KEY_APP_LANGUAGE, config.appLanguage)

            apply()
        }
        // 记录隐藏配置项
        config.hiddenKeys?.let { hiddenKeys.addAll(it) }
        // 记录面板隐藏标志
        hideSettingsPanel = config.hideSettingsPanel ?: false
        hideChannelList = config.hideChannelList ?: false
        hideEpgPanel = config.hideEpgPanel ?: false
        hideOsdPanel = config.hideOsdPanel ?: false
        onConfigApplied?.invoke()
    }

    /** 保存 backup_servers 到本地（修复Bug4）*/
    fun saveBackupServers(context: Context, backupServers: List<String>?) {
        if (backupServers.isNullOrEmpty()) return
        val prefs = context.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        // 转换为 ServerEntry 格式后存入，与 ServerAuthFlowManager.getCandidateServers() 兼容
        val gson = Gson()
        val entries = backupServers.map { mapOf("url" to it, "label" to "") }
        prefs.edit().putString(Prefs.KEY_SERVER_URLS, gson.toJson(entries)).apply()
    }

    /**
     * 判断某个 Prefs key 是否被远程管控（可用于设置界面加锁定标记）
     */
    fun isManaged(prefKey: String): Boolean = managedKeys.contains(prefKey)

    /**
     * 判断某个 Prefs key 是否被远程配置为隐藏（客户端UI中不显示）
     */
    fun isHidden(prefKey: String): Boolean = hiddenKeys.contains(prefKey)

    /**
     * 判断各个面板是否被远程配置为隐藏
     */
    fun isSettingsPanelHidden(): Boolean = hideSettingsPanel
    fun isChannelListHidden(): Boolean = hideChannelList
    fun isEpgPanelHidden(): Boolean = hideEpgPanel
    fun isOsdPanelHidden(): Boolean = hideOsdPanel

    // ── 内部辅助 ────────────────────────────────────────

    private fun applyInt(editor: SharedPreferences.Editor, prefs: SharedPreferences, key: String, value: Int?) {
        if (value != null) {
            editor.putInt(key, value)
            managedKeys.add(key)
        }
    }

    private fun applyBool(editor: SharedPreferences.Editor, prefs: SharedPreferences, key: String, value: Boolean?) {
        if (value != null) {
            editor.putBoolean(key, value)
            managedKeys.add(key)
        }
    }
}
