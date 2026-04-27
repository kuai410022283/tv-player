package com.tvplayer.app.util

import android.app.UiModeManager
import android.content.Context
import android.content.res.Configuration
import android.os.Build
import android.view.View

/**
 * 设备类型检测 + 通用工具
 */
object DeviceUtils {

    enum class DeviceType { TV, PHONE, TABLET }

    /**
     * 检测当前设备类型
     */
    fun getDeviceType(context: Context): DeviceType {
        val uiModeManager = context.getSystemService(Context.UI_MODE_SERVICE) as UiModeManager
        if (uiModeManager.currentModeType == Configuration.UI_MODE_TYPE_TELEVISION) {
            return DeviceType.TV
        }

        val screenLayout = context.resources.configuration.screenLayout and
                Configuration.SCREENLAYOUT_SIZE_MASK
        return when {
            screenLayout >= Configuration.SCREENLAYOUT_SIZE_LARGE -> DeviceType.TABLET
            else -> DeviceType.PHONE
        }
    }

    fun isTV(context: Context): Boolean = getDeviceType(context) == DeviceType.TV
    fun isPhone(context: Context): Boolean = getDeviceType(context) == DeviceType.PHONE

    /**
     * 获取屏幕宽高
     */
    fun getScreenSize(context: Context): Pair<Int, Int> {
        val metrics = context.resources.displayMetrics
        return Pair(metrics.widthPixels, metrics.heightPixels)
    }

    fun isLandscape(context: Context): Boolean {
        return context.resources.configuration.orientation == Configuration.ORIENTATION_LANDSCAPE
    }
}
