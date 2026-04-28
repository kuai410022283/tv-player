# Proguard rules for TVPlayer

# Retrofit
-keepattributes Signature
-keepattributes *Annotation*
-keepattributes Exceptions
-keep class retrofit2.** { *; }
-keepclasseswithmembers class * {
    @retrofit2.http.* <methods>;
}
-dontwarn retrofit2.**

# Gson - 保留所有数据模型
-keep class com.google.gson.** { *; }
-keep class com.tvplayer.app.data.model.** { *; }
-keepclassmembers class com.tvplayer.app.data.model.** { *; }
-keep class com.tvplayer.app.data.api.ClientAuthManager$Companion { *; }

# Gson TypeToken
-keep class com.google.gson.reflect.TypeToken { *; }
-keep class * extends com.google.gson.reflect.TypeToken

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**
-keep class okhttp3.** { *; }
-keepnames class okhttp3.internal.publicsuffix.PublicSuffixDatabase

# ExoPlayer / Media3
-keep class androidx.media3.** { *; }

# Coil
-keep class coil.** { *; }

# Coroutines
-keepnames class kotlinx.coroutines.internal.MainDispatcherFactory {}
-keepnames class kotlinx.coroutines.CoroutineExceptionHandler {}
-keepclassmembers class kotlinx.coroutines.** { volatile <fields>; }

# Keep Application class
-keep class com.tvplayer.app.TVPlayerApp { *; }

# Keep Activities & Services
-keep class com.tvplayer.app.ui.** { *; }
-keep class com.tvplayer.app.service.** { *; }

# Keep API response wrapper
-keep class com.tvplayer.app.data.model.ApiResponse { *; }
-keep class com.tvplayer.app.data.model.PageResponse { *; }
