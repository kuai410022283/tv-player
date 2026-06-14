package com.mediaplayer.app.ui.home.delegates

import android.content.Context
import android.content.Intent
import android.media.AudioManager
import android.net.Uri
import android.view.View
import android.widget.SeekBar
import android.widget.TextView
import android.widget.Toast
import androidx.lifecycle.lifecycleScope
import com.mediaplayer.app.Prefs
import com.mediaplayer.app.R
import com.mediaplayer.app.ui.home.MainActivity
import kotlin.math.max

class SettingsMenuDelegate(private val activity: MainActivity) {

    fun setupSettingsViews() {
        val prefs = activity.getSharedPreferences(Prefs.FILE, Context.MODE_PRIVATE)
        val url = prefs.getString(Prefs.KEY_SERVER_URL, Prefs.DEFAULT_SERVER_URL) ?: ""
        
        val btnSettingsDecoder = activity.findViewById<View>(R.id.btnSettingsDecoder)
        val btnSettingsCore = activity.findViewById<View>(R.id.btnSettingsCore)
        val btnSettingsScale = activity.findViewById<View>(R.id.btnSettingsScale)
        val btnSettingsAutoStart = activity.findViewById<View>(R.id.btnSettingsAutoStart)
        val btnSettingsReverseChannels = activity.findViewById<View>(R.id.btnSettingsReverseChannels)
        
        fun updateDecoderText(mode: Int) {
            activity.findViewById<TextView>(R.id.tvSettingsDecoderValue)?.text = when (mode) {
                Prefs.DECODER_MODE_HARDWARE -> "强制硬解"
                Prefs.DECODER_MODE_SOFTWARE -> "强制软解"
                else -> "自动识别"
            }
        }

        fun updateScaleText(mode: Int) {
            activity.findViewById<TextView>(R.id.tvSettingsScaleValue)?.text = when (mode) {
                Prefs.SCALE_MODE_STRETCH -> "强制 16:9"
                Prefs.SCALE_MODE_4_3 -> "强制 4:3"
                Prefs.SCALE_MODE_16_10 -> "强制 16:10"
                Prefs.SCALE_MODE_CROP -> "放大裁剪"
                Prefs.SCALE_MODE_FILL -> "铺满全屏"
                else -> "原始比例"
            }
        }

        fun updateAutoStartText(enabled: Boolean) {
            activity.findViewById<TextView>(R.id.tvSettingsAutoStartValue)?.text = if (enabled) "开" else "关"
        }
        
        fun updateReverseChannelsText(enabled: Boolean) {
            activity.findViewById<TextView>(R.id.tvSettingsReverseChannelsValue)?.text = if (enabled) "开" else "关"
        }
        
        var currentDecoderMode = prefs.getInt(Prefs.KEY_DECODER_MODE, Prefs.DECODER_MODE_AUTO)
        activity.currentCore = prefs.getInt(Prefs.KEY_PLAYER_CORE, Prefs.PLAYER_CORE_AUTO)
        // 迁移旧版本中 X5 内核的选择（X5 已移除，自动回退到智能切换）
        if (activity.currentCore == 4) {
            activity.currentCore = Prefs.PLAYER_CORE_AUTO
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, activity.currentCore).apply()
        }
        var currentScaleMode = prefs.getInt(Prefs.KEY_SCALE_MODE, Prefs.SCALE_MODE_DEFAULT)
        var currentAutoStart = prefs.getBoolean(Prefs.KEY_AUTO_START, true)
        var currentShowLogo = prefs.getBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, true)
        var currentReverseChannels = prefs.getBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, false)

        updateDecoderText(currentDecoderMode)
        updateCoreText(activity.currentCore)
        updateScaleText(currentScaleMode)
        updateAutoStartText(currentAutoStart)
        updateShowLogoText(currentShowLogo)
        updateReverseChannelsText(currentReverseChannels)
        
        btnSettingsDecoder?.setOnClickListener {
            currentDecoderMode = when (currentDecoderMode) {
                Prefs.DECODER_MODE_AUTO -> Prefs.DECODER_MODE_HARDWARE
                Prefs.DECODER_MODE_HARDWARE -> Prefs.DECODER_MODE_SOFTWARE
                else -> Prefs.DECODER_MODE_AUTO
            }
            updateDecoderText(currentDecoderMode)
            prefs.edit().putInt(Prefs.KEY_DECODER_MODE, currentDecoderMode).apply()
            Toast.makeText(activity, "解码模式已保存，下次播放生效", Toast.LENGTH_SHORT).show()
        }
        
        btnSettingsCore?.setOnClickListener {
            activity.currentCore = when (activity.currentCore) {
                Prefs.PLAYER_CORE_AUTO -> Prefs.PLAYER_CORE_EXO
                Prefs.PLAYER_CORE_EXO -> Prefs.PLAYER_CORE_VLC
                Prefs.PLAYER_CORE_VLC -> Prefs.PLAYER_CORE_IJK
                else -> Prefs.PLAYER_CORE_AUTO
            }
            updateCoreText(activity.currentCore)
            prefs.edit().putInt(Prefs.KEY_PLAYER_CORE, activity.currentCore).apply()
            Toast.makeText(activity, "播放内核已保存，下次播放生效", Toast.LENGTH_SHORT).show()
        }
        
        val btnSettingsShowLogo = activity.findViewById<View>(R.id.btnSettingsShowLogo)
        btnSettingsShowLogo?.setOnClickListener {
            currentShowLogo = !currentShowLogo
            updateShowLogoText(currentShowLogo)
            prefs.edit().putBoolean(Prefs.KEY_SHOW_CHANNEL_LOGO, currentShowLogo).apply()
            
            // 立即生效
            activity.channelAdapter.showLogo = currentShowLogo
            activity.channelAdapter.notifyDataSetChanged()
        }
        
        btnSettingsScale?.setOnClickListener {
            currentScaleMode = when (currentScaleMode) {
                Prefs.SCALE_MODE_DEFAULT -> Prefs.SCALE_MODE_FILL
                Prefs.SCALE_MODE_FILL -> Prefs.SCALE_MODE_STRETCH
                Prefs.SCALE_MODE_STRETCH -> Prefs.SCALE_MODE_16_10
                Prefs.SCALE_MODE_16_10 -> Prefs.SCALE_MODE_4_3
                Prefs.SCALE_MODE_4_3 -> Prefs.SCALE_MODE_CROP
                else -> Prefs.SCALE_MODE_DEFAULT
            }
            updateScaleText(currentScaleMode)
            prefs.edit().putInt(Prefs.KEY_SCALE_MODE, currentScaleMode).apply()
            
            // 立即生效
            activity.playbackSessionManager.setAspectRatio(currentScaleMode)
        }

        btnSettingsAutoStart?.setOnClickListener {
            currentAutoStart = !currentAutoStart
            updateAutoStartText(currentAutoStart)
            prefs.edit().putBoolean(Prefs.KEY_AUTO_START, currentAutoStart).apply()
        }

        btnSettingsReverseChannels?.setOnClickListener {
            currentReverseChannels = !currentReverseChannels
            updateReverseChannelsText(currentReverseChannels)
            prefs.edit().putBoolean(Prefs.KEY_REVERSE_CHANNEL_KEYS, currentReverseChannels).apply()
        }

        val btnSettingsCheckUpdate = activity.findViewById<View>(R.id.btnSettingsCheckUpdate)
        btnSettingsCheckUpdate?.setOnClickListener {
            com.mediaplayer.app.util.UpdateManager.checkUpdate(activity, activity.lifecycleScope, true)
        }

        val etSettingsUrl = activity.findViewById<TextView>(R.id.etSettingsUrl)
        etSettingsUrl?.text = url
        
        val sbSettingsCache = activity.findViewById<SeekBar>(R.id.sbSettingsCache)
        val tvSettingsCacheValue = activity.findViewById<TextView>(R.id.tvSettingsCacheValue)

        val cacheMs = prefs.getInt(Prefs.KEY_NETWORK_CACHE, Prefs.DEFAULT_NETWORK_CACHE)
        val progress = if (cacheMs == 0) 0 else (cacheMs / 50).coerceIn(1, 100)
        sbSettingsCache?.progress = progress
        tvSettingsCacheValue?.text = if (cacheMs == 0) " 自动" else " ${"%.2f".format(cacheMs / 1000f)} 秒"

        sbSettingsCache?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                val newCacheMs = if (progress == 0) 0 else progress * 50
                tvSettingsCacheValue?.text = if (newCacheMs == 0) " 自动" else " ${"%.2f".format(newCacheMs / 1000f)} 秒"
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {
                val p = seekBar?.progress ?: 0
                val newCacheMs = if (p == 0) 0 else p * 50
                prefs.edit().putInt(Prefs.KEY_NETWORK_CACHE, newCacheMs).apply()
                activity.playbackSessionManager.setCacheDuration(newCacheMs)
                Toast.makeText(activity, "网络缓存已保存，下次播放生效", Toast.LENGTH_SHORT).show()
            }
        })

        // 音量设置
        val audioManager = activity.getSystemService(android.content.Context.AUDIO_SERVICE) as android.media.AudioManager
        val maxVolume = audioManager.getStreamMaxVolume(android.media.AudioManager.STREAM_MUSIC)
        val currentVolume = audioManager.getStreamVolume(android.media.AudioManager.STREAM_MUSIC)
        val sbVolume = activity.findViewById<SeekBar>(R.id.sbSettingsVolume)
        sbVolume?.max = maxVolume
        sbVolume?.progress = currentVolume
        sbVolume?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser) {
                    audioManager.setStreamVolume(android.media.AudioManager.STREAM_MUSIC, progress, 0)
                }
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {}
        })

        // 亮度设置
        val sbBrightness = activity.findViewById<SeekBar>(R.id.sbSettingsBrightness)
        sbBrightness?.max = 100
        sbBrightness?.progress = ((activity.window.attributes.screenBrightness.coerceAtLeast(0.01f)) * 100).toInt().coerceIn(1, 100)
        sbBrightness?.setOnSeekBarChangeListener(object : android.widget.SeekBar.OnSeekBarChangeListener {
            override fun onProgressChanged(seekBar: android.widget.SeekBar?, progress: Int, fromUser: Boolean) {
                if (fromUser) {
                    val lp = activity.window.attributes
                    lp.screenBrightness = max(0.01f, progress / 100f)
                    activity.window.attributes = lp
                }
            }
            override fun onStartTrackingTouch(seekBar: android.widget.SeekBar?) {}
            override fun onStopTrackingTouch(seekBar: android.widget.SeekBar?) {}
        })
        
        activity.findViewById<TextView>(R.id.tvQQGroup)?.setOnClickListener {
            try {
                // 使用 mqqapi 协议直接唤起手机 QQ 加群页面
                val intent = Intent(Intent.ACTION_VIEW, Uri.parse("mqqapi://card/show_pslcard?src_type=internal&version=1&uin=864744268&card_type=group&source=qrcode"))
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                activity.startActivity(intent)
            } catch (e: Exception) {
                // 未安装 QQ 或拉起失败，复制群号到剪贴板
                val clipboard = activity.getSystemService(android.content.Context.CLIPBOARD_SERVICE) as android.content.ClipboardManager
                val clip = android.content.ClipData.newPlainText("QQ群", "864744268")
                clipboard.setPrimaryClip(clip)
                Toast.makeText(activity, "未检测到QQ应用，已复制群号: 864744268", Toast.LENGTH_SHORT).show()
            }
        }
    }


    private fun updateCoreText(core: Int) {
        activity.findViewById<TextView>(R.id.tvSettingsCoreValue)?.text = when (core) {
            Prefs.PLAYER_CORE_EXO -> "ExoPlayer"
            Prefs.PLAYER_CORE_VLC -> "VLC"
            Prefs.PLAYER_CORE_IJK -> "IJKPlayer"
            else -> "智能切换"
        }
    }


    private fun updateShowLogoText(show: Boolean) {
        activity.findViewById<TextView>(R.id.tvSettingsShowLogoValue)?.text = if (show) "显示" else "隐藏"
    }




}
