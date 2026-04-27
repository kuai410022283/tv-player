# Proguard rules for TVPlayer

# Retrofit
-keepattributes Signature
-keepattributes *Annotation*
-keep class retrofit2.** { *; }
-keepclasseswithmembers class * {
    @retrofit2.http.* <methods>;
}

# Gson
-keep class com.google.gson.** { *; }
-keep class com.tvplayer.app.data.model.** { *; }

# OkHttp
-dontwarn okhttp3.**
-dontwarn okio.**

# ExoPlayer
-keep class androidx.media3.** { *; }

# Room
-keep class * extends androidx.room.RoomDatabase
-keep @androidx.room.Entity class *
