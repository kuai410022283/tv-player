# Proguard rules for TVPlayer

# Retrofit
-keepattributes Signature
-keepattributes *Annotation*
-keep class retrofit2.** { *; }
-keepclasseswithmembers class * {
    @retrofit2.http.* <methods>;
}

# Gson - 保留所有数据模型
-keep class com.google.gson.** { *; }
-keep class com.tvplayer.app.data.model.** { *; }
-keepclassmembers class com.tvplayer.app.data.model.** { *; }
-keep class com.tvplayer.app.data.api.ClientAuthManager$Companion { *; }

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**
-keep class okhttp3.** { *; }

# ExoPlayer / Media3
-keep class androidx.media3.** { *; }
-keep class com.google.android.exoplayer2.** { *; }

# Room
-keep class * extends androidx.room.RoomDatabase
-keep @androidx.room.Entity class *

# Coil
-keep class coil.** { *; }

# Coroutines
-keepnames class kotlinx.coroutines.internal.MainDispatcherFactory {}
-keepnames class kotlinx.coroutines.CoroutineExceptionHandler {}

# Keep Application class
-keep class com.tvplayer.app.TVPlayerApp { *; }

# Keep Activities & Services
-keep class com.tvplayer.app.ui.** { *; }
-keep class com.tvplayer.app.service.** { *; }
