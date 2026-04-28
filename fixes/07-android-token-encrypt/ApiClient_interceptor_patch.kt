// ========================================
// ApiClient.kt — Interceptor 修改为使用加密存储的 Token
// ========================================

// ★ 修改 authInterceptor 部分：

val authInterceptor = Interceptor { chain ->
    val original = chain.request()
    val skipAuth = original.url.encodedPath.contains("client/register") ||
                   original.url.encodedPath.contains("client/verify")

    val request = if (!skipAuth) {
        // ★ 使用 EncryptedSharedPreferences 读取 token
        val masterKey = MasterKey.Builder(TVPlayerApp.instance)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        val securePrefs = EncryptedSharedPreferences.create(
            TVPlayerApp.instance,
            "${Prefs.FILE}_secure",
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM
        )
        val token = securePrefs.getString("client_access_token", null)
            ?: // 兼容旧存储
            TVPlayerApp.instance.getSharedPreferences(Prefs.FILE, 0)
                .getString(Prefs.KEY_ACCESS_TOKEN, null)

        if (token != null) {
            original.newBuilder()
                .header("X-Client-Token", token)
                .header("Authorization", "Bearer $token")
                .build()
        } else {
            original
        }
    } else {
        original
    }

    chain.proceed(request)
}
