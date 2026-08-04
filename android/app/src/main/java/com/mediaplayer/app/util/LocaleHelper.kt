package com.mediaplayer.app.util

import android.content.Context
import android.content.res.Configuration
import android.os.Build
import java.util.Locale

object LocaleHelper {
    fun wrap(context: Context, languageCode: String): Context {
        if (languageCode == "auto") {
            return context
        }
        val locale = getLocale(languageCode)
        Locale.setDefault(locale)
        val resources = context.resources
        val configuration = resources.configuration
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            configuration.setLocale(locale)
            val localeList = android.os.LocaleList(locale)
            android.os.LocaleList.setDefault(localeList)
            configuration.setLocales(localeList)
            return context.createConfigurationContext(configuration)
        } else {
            configuration.locale = locale
            resources.updateConfiguration(configuration, resources.displayMetrics)
            return context
        }
    }

    private fun getLocale(languageCode: String): Locale {
        return when (languageCode) {
            "zh-CN" -> Locale.SIMPLIFIED_CHINESE
            "zh-TW" -> Locale.TRADITIONAL_CHINESE
            "en" -> Locale.ENGLISH
            "ko" -> Locale.KOREAN
            "ja" -> Locale.JAPANESE
            "ru" -> Locale("ru")
            "de" -> Locale.GERMAN
            "fr" -> Locale.FRENCH
            "es" -> Locale("es")
            "it" -> Locale.ITALIAN
            else -> Locale.getDefault()
        }
    }
}
