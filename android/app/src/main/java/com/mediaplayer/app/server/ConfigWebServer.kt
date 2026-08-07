package com.mediaplayer.app.server

import android.content.Context
import fi.iki.elonen.NanoHTTPD
import java.net.ServerSocket
import java.net.URL

/**
 * 嵌入式 Web 配置服务器。
 * 启动后用户通过浏览器访问 http://<电视IP>:<port>/ 填写服务器配置。
 *
 * @param context    Android Context
 * @param port       监听端口。传入 0 表示自动查找可用端口（从 9528 开始递增探测）
 * @param onUrlSaved 配置保存后的回调，参数为解析后的服务器地址列表（第一项为主服务器）
 */
class ConfigWebServer(
    private val context: Context,
    port: Int = 0,
    private val onUrlSaved: (List<String>) -> Unit
) : NanoHTTPD(if (port > 0) port else findAvailablePort(9528)) {

    /** 实际监听端口（与传入 NanoHTTPD 构造函数的端口一致）。 */
    val actualPort: Int = if (port > 0) port else findAvailablePort(9528)

    /**
     * 授权状态，供 /status 端点读取。
     * idle → 连接中 → pending/approved/failed
     */
    @Volatile var authStatus: String = "idle"


    /** 更新授权状态并通知等待中的手机页面 */
    fun updateAuthStatus(status: String) {
        authStatus = status
    }

    init {
        globalPort = actualPort
    }



    override fun serve(session: IHTTPSession): Response {
        val method = session.method
        val uri = session.uri

        if (method == Method.GET && uri == "/") {
            return newFixedLengthResponse(getHtmlForm())
        }

        // 授权状态轮询端点，供手机页面查询 TV 注册进度
        if (method == Method.GET && uri == "/status") {
            val resp = newFixedLengthResponse(Response.Status.OK, "application/json", """{"status":"$authStatus"}""")
            resp.addHeader("Cache-Control", "no-cache, no-store, must-revalidate")
            resp.addHeader("Pragma", "no-cache")
            return resp
        }

        if (method == Method.POST && uri == "/save") {
            try {
                val map = HashMap<String, String>()
                session.parseBody(map)
                val params = session.parameters
                
                val localEnabled = params["local_enabled"]?.firstOrNull()?.toBoolean() ?: false
                val localPlaylist = params["local_playlist"]?.firstOrNull()?.trim() ?: ""
                val localEpg = params["local_epg"]?.firstOrNull()?.trim() ?: ""
                
                val rawInput = params["server_url"]?.firstOrNull()?.trim()
                val location = params["location"]?.firstOrNull()?.trim()

                val prefs = context.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, Context.MODE_PRIVATE)

                // 保存本地数据源配置
                prefs.edit().apply {
                    putBoolean(com.mediaplayer.app.Prefs.KEY_LOCAL_SOURCE_ENABLED, localEnabled)
                    putString(com.mediaplayer.app.Prefs.KEY_LOCAL_PLAYLIST_URL, localPlaylist)
                    putString(com.mediaplayer.app.Prefs.KEY_LOCAL_EPG_URL, localEpg)
                }.apply()

                if (localEnabled && localPlaylist.isNotEmpty()) {
                    // 本地源开启直接生效，无需验证服务端授权
                    authStatus = "approved"
                    // 广播通知主界面刷新本地列表
                    onUrlSaved(emptyList())
                    return newFixedLengthResponse(getSuccessHtml())
                }

                if (rawInput.isNullOrEmpty()) {
                    return newFixedLengthResponse(Response.Status.BAD_REQUEST, MIME_HTML, getErrorHtml("请输入服务器配置信息"))
                }

                val resolvedUrls = parseMultipleUrls(rawInput)
                if (resolvedUrls.isEmpty()) {
                    return newFixedLengthResponse(Response.Status.BAD_REQUEST, MIME_HTML, getErrorHtml("所有配置信息均无效，请检查后重试"))
                }

                if (!location.isNullOrEmpty()) {
                    prefs.edit().putString("device_location", location).apply()
                } else {
                    prefs.edit().remove("device_location").apply()
                }

                authStatus = "connecting"
                onUrlSaved(resolvedUrls)
                return newFixedLengthResponse(getSuccessHtml())
            } catch (e: Exception) {
                e.printStackTrace()
                return newFixedLengthResponse(Response.Status.INTERNAL_ERROR, MIME_PLAINTEXT, "Error parsing body")
            }
        }


        return newFixedLengthResponse(Response.Status.NOT_FOUND, MIME_PLAINTEXT, "Not Found")
    }

    // ──────────────────────────────────────────────
    //  端口探测
    // ──────────────────────────────────────────────

    companion object {
        var globalPort: Int = 9528
            private set

        /**
         * 从 [startPort] 开始递增探测可用端口，避免与已有服务冲突。
         * 最多尝试 10 个端口。
         */
        private fun findAvailablePort(startPort: Int = 9528): Int {
            for (port in startPort until startPort + 10) {
                try {
                    val ss = ServerSocket(port)
                    ss.close()
                    return port
                } catch (_: Exception) {
                    // 端口被占用，尝试下一个
                }
            }
            // 所有端口均被占用，让 OS 分配随机端口
            return 0
        }
    }

    // ──────────────────────────────────────────────
    //  多服务器地址解析
    // ──────────────────────────────────────────────

    /**
     * 将用户输入的文本分割为多个候选服务器地址，逐个解析并验证。
     *
     * 分隔符：换行、逗号、分号、空格
     * 每个条目优先尝试 Base64 解码，失败则直接作为 URL 处理。
     */
    private fun parseMultipleUrls(input: String): List<String> {
        val parts = input.split(Regex("[\\n,;]+"))
            .flatMap { it.split(Regex("\\s+")) }
            .map { it.trim() }
            .filter { it.isNotEmpty() }

        if (parts.isEmpty()) return emptyList()

        val valid = mutableListOf<String>()
        for (item in parts) {
            val url = resolveSingleUrl(item)
            if (url != null && url !in valid) {
                valid.add(url)
            }
        }
        return valid
    }

    /**
     * 解析单条输入为有效的服务器 URL。
     *
     * 流程：
     * 1. 尝试 Base64 解码
     * 2. 解码成功则验证 URL
     * 3. 解码失败则直接验证原始输入
     */
    private fun resolveSingleUrl(input: String): String? {
        val candidates = buildList {
            val decoded = try {
                val bytes = android.util.Base64.decode(input, android.util.Base64.DEFAULT)
                String(bytes, Charsets.UTF_8).trim()
            } catch (_: Exception) {
                null
            }
            if (decoded != null) add(decoded)
            add(input)
        }

        for (candidate in candidates) {
            val normalized = normalizeUrl(candidate)
            if (isValidHttpUrl(normalized)) {
                return normalized
            }
        }
        return null
    }

    /** 补全 URL 协议前缀。 */
    private fun normalizeUrl(url: String): String {
        val trimmed = url.trim().trimEnd('/')
        if (trimmed.isEmpty()) return trimmed
        if (!trimmed.startsWith("http://", ignoreCase = true) &&
            !trimmed.startsWith("https://", ignoreCase = true)) {
            return "http://$trimmed"
        }
        return trimmed
    }

    /** 使用 java.net.URL 严格校验是否为合法的 HTTP(S) URL。 */
    private fun isValidHttpUrl(url: String): Boolean {
        return try {
            val parsed = URL(url)
            val protocol = parsed.protocol.lowercase()
            (protocol == "http" || protocol == "https") &&
                parsed.host.isNotEmpty() &&
                (parsed.host.indexOf('.') > 0 || parsed.host == "localhost" || parsed.host.contains(':'))
        } catch (_: Exception) {
            false
        }
    }

    // ──────────────────────────────────────────────
    //  HTML 页面
    // ──────────────────────────────────────────────

    private fun getErrorHtml(message: String): String {
        return """
            <!DOCTYPE html>
            <html lang="zh-CN">
            <head>
                <meta charset="UTF-8">
                <meta name="viewport" content="width=device-width, initial-scale=1.0">
                <title>配置错误</title>
                <style>
                    body { font-family: -apple-system, sans-serif; background: #f4f4f5; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
                    .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); width: 90%; max-width: 400px; text-align: center; }
                    h2 { color: #dc3545; margin-top: 0; }
                    p { color: #666; font-size: 14px; white-space: pre-wrap; }
                    a { display: inline-block; margin-top: 16px; padding: 10px 24px; background: #007bff; color: white; text-decoration: none; border-radius: 6px; font-size: 14px; }
                    a:hover { background: #0056b3; }
                </style>
            </head>
            <body>
                <div class="card">
                    <h2>配置无效</h2>
                    <p>${message}</p>
                    <a href="/">返回重试</a>
                </div>
            </body>
            </html>
        """.trimIndent()
    }

    private fun getHtmlForm(): String {
        val prefs = context.getSharedPreferences(com.mediaplayer.app.Prefs.FILE, Context.MODE_PRIVATE)
        val localEnabled = prefs.getBoolean(com.mediaplayer.app.Prefs.KEY_LOCAL_SOURCE_ENABLED, false)
        val playlistUrl = prefs.getString(com.mediaplayer.app.Prefs.KEY_LOCAL_PLAYLIST_URL, "") ?: ""
        val epgUrl = prefs.getString(com.mediaplayer.app.Prefs.KEY_LOCAL_EPG_URL, "") ?: ""
        val serverUrl = prefs.getString(com.mediaplayer.app.Prefs.KEY_SERVER_URL, "") ?: ""
        val location = prefs.getString("device_location", "") ?: ""

        return """
            <!DOCTYPE html>
            <html lang="zh-CN">
            <head>
                <meta charset="UTF-8">
                <meta name="viewport" content="width=device-width, initial-scale=1.0">
                <title>服务器配置</title>
                <style>
                    body { font-family: -apple-system, sans-serif; background: #f4f4f5; display: flex; justify-content: center; align-items: center; min-height: 100vh; margin: 0; padding: 20px 0; box-sizing: border-box; }
                    .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.05); width: 90%; max-width: 420px; text-align: center; }
                    h2 { margin-top: 0; color: #333; }
                    input, textarea { width: 100%; padding: 12px; margin: 12px 0; border: 1px solid #ddd; border-radius: 6px; box-sizing: border-box; font-size: 15px; }
                    textarea { resize: vertical; min-height: 100px; }
                    .hint { text-align: left; color: #888; font-size: 12px; margin: -8px 0 12px; line-height: 1.5; }
                    .switch-container { display: flex; justify-content: space-between; align-items: center; background: #f8f9fa; padding: 12px; border-radius: 6px; margin: 12px 0; border: 1px solid #eee; }
                    .switch-label { font-size: 14px; color: #495057; font-weight: bold; }
                    .switch { position: relative; display: inline-block; width: 44px; height: 24px; }
                    .switch input { opacity: 0; width: 0; height: 0; }
                    .slider { position: absolute; cursor: pointer; top: 0; left: 0; right: 0; bottom: 0; background-color: #ccc; transition: .3s; border-radius: 24px; }
                    .slider:before { position: absolute; content: ""; height: 18px; width: 18px; left: 3px; bottom: 3px; background-color: white; transition: .3s; border-radius: 50%; }
                    input:checked + .slider { background-color: #007bff; }
                    input:checked + .slider:before { transform: translateX(20px); }
                    button { width: 100%; padding: 12px; background: #007bff; color: white; border: none; border-radius: 6px; font-size: 16px; cursor: pointer; transition: 0.3s; margin-top: 12px; }
                    button:hover { background: #0056b3; }
                    .form-section { display: none; transition: all 0.3s ease; }
                    .active-section { display: block; }
                </style>
            </head>
            <body>
                <div class="card">
                    <h2>欢迎使用</h2>
                    <p style="color:#666; font-size:14px; margin-bottom: 20px;">配置播放器的频道与授权数据源</p>
                    <form action="/save" method="post" id="configForm">
                        
                        <div class="switch-container">
                            <span class="switch-label">启用用户本地数据源</span>
                            <label class="switch">
                                <input type="checkbox" name="local_enabled" id="localEnabled" value="true" ${if (localEnabled) "checked" else ""}>
                                <span class="slider"></span>
                            </label>
                        </div>

                        <!-- 本地源配置区块 -->
                        <div id="localSection" class="form-section ${if (localEnabled) "active-section" else ""}">
                            <input type="text" name="local_playlist" value="${playlistUrl}" placeholder="用户源地址 (支持 M3U / TXT)">
                            <div class="hint">填写您的 M3U 或 TXT 格式播放列表 URL。</div>
                            <input type="text" name="local_epg" value="${epgUrl}" placeholder="自定义 EPG 地址 (XML / XML.GZ)">
                            <div class="hint">可选：XMLTV 电子节目单地址，支持 .xml.gz 压缩包。</div>
                        </div>

                        <!-- 原有服务器授权码区块 -->
                        <div id="serverSection" class="form-section ${if (!localEnabled) "active-section" else ""}">
                            <textarea name="server_url" placeholder="请在此粘贴服务商提供的授权码&#10;支持多个授权码，自动切换备用服务器">${serverUrl}</textarea>
                            <div class="hint">第一行为主服务器，每行填写一个。</div>
                            <input type="text" name="location" value="${location}" placeholder="安装位置 (例如：一号楼101房间)">
                        </div>

                        <button type="submit">保存配置</button>
                    </form>
                </div>

                <script>
                    const localCheckbox = document.getElementById('localEnabled');
                    const localSection = document.getElementById('localSection');
                    const serverSection = document.getElementById('serverSection');
                    
                    localCheckbox.addEventListener('change', function() {
                        if (this.checked) {
                            localSection.classList.add('active-section');
                            serverSection.classList.remove('active-section');
                            document.querySelector('[name="server_url"]').required = false;
                            document.querySelector('[name="location"]').required = false;
                            document.querySelector('[name="local_playlist"]').required = true;
                        } else {
                            localSection.classList.remove('active-section');
                            serverSection.classList.add('active-section');
                            document.querySelector('[name="server_url"]').required = true;
                            document.querySelector('[name="location"]').required = true;
                            document.querySelector('[name="local_playlist"]').required = false;
                        }
                    });

                    // 初始运行一次，设定 required 校验
                    if (localCheckbox.checked) {
                        document.querySelector('[name="local_playlist"]').required = true;
                    } else {
                        document.querySelector('[name="server_url"]').required = true;
                        document.querySelector('[name="location"]').required = true;
                    }
                </script>
            </body>
            </html>
        """.trimIndent()
    }

    private fun getSuccessHtml(): String {
        return """
            <!DOCTYPE html>
            <html lang="zh-CN">
            <head>
                <meta charset="UTF-8">
                <meta name="viewport" content="width=device-width, initial-scale=1.0">
                <title>配置结果</title>
                <style>
                    body { font-family: -apple-system, sans-serif; background: #f4f4f5; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
                    .card { background: white; padding: 2rem; border-radius: 12px; box-shadow: 0 4px 6px rgba(0,0,0,0.1); width: 90%; max-width: 400px; text-align: center; }
                    h2 { margin-top: 0; }
                    p { color: #666; font-size: 14px; }
                    .ok { color: #28a745; }
                    .pending { color: #f0ad4e; }
                    .fail { color: #dc3545; }
                    .spinner { display: inline-block; width: 20px; height: 20px; border: 3px solid #ddd; border-top-color: #007bff; border-radius: 50%; animation: spin 0.8s linear infinite; margin-right: 8px; vertical-align: middle; }
                    @keyframes spin { to { transform: rotate(360deg); } }
                    a { display: inline-block; margin-top: 12px; padding: 10px 24px; background: #007bff; color: white; text-decoration: none; border-radius: 6px; font-size: 14px; }
                </style>
            </head>
            <body>
                <div class="card">
                    <div id="result">
                        <h2 style="color:#007bff"><span class="spinner"></span>正在连接服务器...</h2>
                        <p>电视端已收到授权码，正在尝试连接后端服务器，请稍候...</p>
                    </div>
                </div>
                <script>
                    var count = 0;
                    var timer = setInterval(function() {
                        count++;
                        fetch('/status').then(function(r) { return r.json(); }).then(function(d) {
                            var el = document.getElementById('result');
                            if (d.status === 'approved') {
                                clearInterval(timer);
                                el.innerHTML = '<h2 class="ok">授权成功</h2><p>电视端已成功连接服务器，可以关闭此页面了。</p>';
                            } else if (d.status === 'pending') {
                                clearInterval(timer);
                                el.innerHTML = '<h2 class="pending">等待审批</h2><p>设备已注册，等待管理员审批通过后电视端将自动进入。</p>';
                            } else if (d.status === 'failed') {
                                clearInterval(timer);
                                el.innerHTML = '<h2 class="fail">连接失败</h2><p>电视端无法连接后端服务器，请检查授权码是否正确、网络是否通畅。</p><a href="/">返回重试</a>';
                            } else if (d.status === 'retrying') {
                                el.innerHTML = '<h2 style="color:#f0ad4e"><span class="spinner"></span>连接失败，正在重试...</h2><p>电视端无法连接服务器，15秒后将自动重试，请耐心等待。</p>';
                            }
                            // connecting → 继续轮询
                        }).catch(function() {});
                        if (count > 30) { clearInterval(timer); }
                    }, 2000);
                </script>
            </body>
            </html>
        """.trimIndent()
    }
}