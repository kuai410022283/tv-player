#!/bin/bash

echo "🚀 开始编译 Android TV 客户端 Release APK..."

# 修复 JAVA_HOME 指向错误的问题（去掉末尾的 \bin）
export JAVA_HOME="D:/Program Files/Android/Android Studio/jbr"

# 确保 gradlew 具有可执行权限
chmod +x ./gradlew

# ─────────────────────────────────────────────────────────────
# 【签名配置】支持三种方式（优先级从高到低）：
#
# 方式①：已设置环境变量（与 GitHub Actions 完全一致）
#   export ANDROID_SIGNING_KEYSTORE=/path/to/release.keystore
#   export ANDROID_SIGNING_PASSWORD=你的store密码
#   export ANDROID_SIGNING_KEY_ALIAS=你的key别名
#   export ANDROID_SIGNING_KEY_PASSWORD=你的key密码
#
# 方式②：同目录下存在 keystore.properties 文件（推荐本地使用）
#   创建 android/keystore.properties（此文件已在 .gitignore 中，不会提交）：
#   KEYSTORE_PATH=D:/path/to/release.keystore
#   KEYSTORE_PASSWORD=你的store密码
#   KEY_ALIAS=你的key别名
#   KEY_PASSWORD=你的key密码
#
# 方式③：以上都没有 → 自动用 debug 签名（仅本地测试）
# ─────────────────────────────────────────────────────────────

KEYSTORE_PROPS="keystore.properties"

# 尝试从 keystore.properties 加载（如果环境变量尚未设置）
if [ -z "$ANDROID_SIGNING_KEYSTORE" ] && [ -f "$KEYSTORE_PROPS" ]; then
    echo "📋 检测到 keystore.properties，加载本地签名配置..."
    while IFS='=' read -r key value; do
        # 忽略注释和空行
        [[ "$key" =~ ^#.*$ || -z "$key" ]] && continue
        key=$(echo "$key" | xargs)   # 去首尾空格
        value=$(echo "$value" | xargs)
        case "$key" in
            KEYSTORE_PATH)     export ANDROID_SIGNING_KEYSTORE="$value" ;;
            KEYSTORE_PASSWORD) export ANDROID_SIGNING_PASSWORD="$value" ;;
            KEY_ALIAS)         export ANDROID_SIGNING_KEY_ALIAS="$value" ;;
            KEY_PASSWORD)      export ANDROID_SIGNING_KEY_PASSWORD="$value" ;;
        esac
    done < "$KEYSTORE_PROPS"
fi

# 显示当前签名状态
echo "------------------------------------------------"
if [ -n "$ANDROID_SIGNING_KEYSTORE" ] && [ -f "$ANDROID_SIGNING_KEYSTORE" ]; then
    echo "🔐 签名方式: Release 正式签名"
    echo "   Keystore : $ANDROID_SIGNING_KEYSTORE"
    echo "   Key Alias: $ANDROID_SIGNING_KEY_ALIAS"
else
    echo "⚠️  签名方式: Debug 签名（APK 无法覆盖 Release 安装）"
    echo "   如需正式签名，请参考脚本顶部注释创建 keystore.properties"
fi
echo "------------------------------------------------"

# ─────────────────────────────────────────────────────────────
# 【版本号】默认自动递增，传 --no-bump 参数可跳过
# ─────────────────────────────────────────────────────────────
BUMP_VERSION=true
for arg in "$@"; do
    if [ "$arg" == "--no-bump" ]; then
        BUMP_VERSION=false
        break
    fi
done

if [ "$BUMP_VERSION" = true ]; then
    GRADLE_FILE="app/build.gradle"
    if [ -f "$GRADLE_FILE" ]; then
        # 提取旧 versionCode 并加 1
        OLD_VC=$(grep -oE 'versionCode [0-9]+' "$GRADLE_FILE" | awk '{print $2}')
        if [ -n "$OLD_VC" ]; then
            NEW_VC=$((OLD_VC + 1))
            echo "🔄 自动升级 versionCode: $OLD_VC -> $NEW_VC"
            sed -i -E "s/versionCode [0-9]+/versionCode $NEW_VC/" "$GRADLE_FILE"
        fi

        # 提取旧 versionName 并在最后一位自动加 1 (例如 1.0.29 -> 1.0.30)
        OLD_VN=$(grep -oE 'versionName "[^"]+"' "$GRADLE_FILE" | cut -d'"' -f2)
        if [ -n "$OLD_VN" ]; then
            IFS='.' read -r v1 v2 v3 <<< "$OLD_VN"
            if [ -n "$v3" ]; then
                NEW_VN="$v1.$v2.$((v3 + 1))"
                echo "🔄 自动升级 versionName: $OLD_VN -> $NEW_VN"
                sed -i -E "s/versionName \"[^\"]+\"/versionName \"$NEW_VN\"/" "$GRADLE_FILE"
            fi
        fi
        echo "------------------------------------------------"
    fi
else
    echo "ℹ️  跳过自动升级版本号"
    echo "------------------------------------------------"
fi

# 执行编译
./gradlew assembleRelease --no-build-cache

if [ $? -ne 0 ]; then
    echo "❌ 编译失败，请检查上面的报错信息！"
    exit 1
else
    echo "✅ 编译成功！"
    if [ -n "$ANDROID_SIGNING_KEYSTORE" ] && [ -f "$ANDROID_SIGNING_KEYSTORE" ]; then
        echo "🔐 APK 已使用 Release 正式签名"
    else
        echo "⚠️  APK 使用 Debug 签名（注意：无法覆盖正式签名版本安装）"
    fi
    echo "📂 APK 位于: app/build/outputs/apk/release/"
    explorer.exe "app\\build\\outputs\\apk\\release" 2>/dev/null || echo "请手动前往 app/build/outputs/apk/release/ 查找 APK"
fi
