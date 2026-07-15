package com.mediaplayer.app.server

import java.util.regex.Pattern

/**
 * 🎬 M3U8 去广告特征库引擎 (AdBlock Feature Database)
 *
 * 专门用于存储和判断视频流切片是否为广告。
 * 后续可轻易剥离或改造为通过云端动态下发 JSON 规则。
 */
object AdBlockRuleEngine {

    // 策略1：常见广告域名或路径特征（URL 关键字黑名单）
    private val AD_URL_PATTERN = Pattern.compile("(?i)(/ad/|advert|promo|p3p\\.)")

    // 策略2：已知广告特征哈希库 (SSAI 混淆广告特征)
    // 比如 dytt-cine 常用的插播广告片段的 hash
    private val AD_HASH_BLACKLIST = setOf(
        // === dytt-cine.com ===
        "5fbd8ce64f225ad5603b244ff147eca5bd81415b298b324b03d21d29f06d3fd1", 
        "9eaea17cfacf22236e97d19f537aa77e00503b8846367d891b61f43907406bba", 
        "f74c2233e945b24b0698b9b2420c4ebc7cb2c7927cc27b2cf18c86ef09f8735f", 
        "1134ef05010e4c62c6bd537860720daf971425f4a7e0d290e337b1fc4cb6d3b3", 
        "4648a1abd8d3353ca1cde729b2e9f3229cafb3351a3d3380014a5ea3120195de",
        // === dytt-cinema.com ===
        "c1035894873bd899ebc964dbc15e8988",
        "9a21707cc993194fa781d61acb0bfbc0",
        "573b8bdbea8f2973e39dd927e81ff5e8",
        "fbc27f06bb4207b978579fc4b3b73c77",
        "250b72ae8bdf2eb2a1d173052c7fbaac",
        // === dytt-live.com ===
        "da85500480e396356b0aec9abc96cb50",
        "42b78b0561c2780e920d0f4119d671eb",
        "c8c49e2cf4b9a9d701df994bd572d42f",
        "3a9d949cf94e9f3b145fa3565fb5b7e2",
        "61e4334335c91b5c87a552804c86e06b"
    )

    // 策略3：已知的混淆广告 TS 文件名黑名单
    private val AD_FILENAME_BLACKLIST = setOf(
        // === dytt-cine.com ===
        "014e5b0a6b1334c2e9f2758f6ab51d06",
        "ae8c16e5cb6e135da426043f1afb9edd",
        "c303cc9ea2bc35b127f630ccc26e323f",
        "991aabc9f657a6b9c9b00e8253910b55",
        "d161e6029ab6ff7e24fae54f4178fde4",
        // === dytt-cinema.com ===
        "f40d6ec225a5284f45bc9349e8763bc7",
        "5856eee1915e4483458a7d9b50eae233",
        "177cdc43dcc7a477df4c0371233993e1",
        "67d33ba6291684b1c05a342d702ae28e",
        "776b395201294f0f2909d3d92dfcb118",
        // === dytt-live.com ===
        "1a8b2daf435f7effb6087dc9a678e448",
        "16731937764395639ee08fd371e8dfca",
        "291c806b16667a977670b4f27523d7dd",
        "4ee714c22b58ce19fda162ed5962abfe",
        "9cef53bfafc171b2ee9706fc3eb114f7"
    )

    // 策略4：终极必杀技 - 毫秒级切片时长指纹库
    // 即使混淆文件名和 hash，相同广告压制出的切片时长（精确到小数点后3位）也是完全一致的！
    private val AD_DURATION_FINGERPRINTS = setOf(
        5.567,
        2.933,
        5.700,
        3.333,
        1.533
    )

    /**
     * 判断给定的切片 URL 是否命中广告特征库
     */
    fun isAdSegment(url: String, duration: Double? = null): Boolean {
        // 匹配广告正则
        if (AD_URL_PATTERN.matcher(url).find()) {
            return true
        }

        // 提取并匹配 hash 参数库
        val hashMatch = Regex("hash=([a-fA-F0-9]+)").find(url)
        if (hashMatch != null && AD_HASH_BLACKLIST.contains(hashMatch.groupValues[1])) {
            return true
        }

        // 提取并匹配 TS 文件名库
        val nameMatch = Regex("([a-fA-F0-9]{32})\\.ts").find(url)
        if (nameMatch != null && AD_FILENAME_BLACKLIST.contains(nameMatch.groupValues[1])) {
            return true
        }

        // 终极匹配：时长指纹匹配
        // 由于不同节点的 M3U8 可能对 EXTINF 保留的小数位数不同（如 5.567 和 5.566667），我们需要一个容差
        if (duration != null) {
            for (fingerprint in AD_DURATION_FINGERPRINTS) {
                if (kotlin.math.abs(duration - fingerprint) < 0.01) {
                    com.mediaplayer.app.util.RemoteLogger.i("AdBlock", "命中隐藏广告时长指纹拦截: ${duration}s")
                    return true
                }
            }
        }

        return false
    }
}
