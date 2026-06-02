#!/bin/bash

echo "🚀 开始编译 Android TV 客户端 Release APK..."

# 修复 JAVA_HOME 指向错误的问题（去掉末尾的 \bin）
export JAVA_HOME="D:/Program Files/Android/Android Studio/jbr"

# 确保 gradlew 具有可执行权限
chmod +x ./gradlew

# 是否自动追加版本号？（可通过 --bump 参数开启）
BUMP_VERSION=true
for arg in "$@"; do
    if [ "$arg" == "--bump" ] || [ "$arg" == "-b" ]; then
        BUMP_VERSION=true
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

        # 提取旧 versionName 并在最后一位自动加 1 (例如 1.0.0 -> 1.0.1)
        OLD_VN=$(grep -oE 'versionName "[^"]+"' "$GRADLE_FILE" | cut -d'"' -f2)
        if [ -n "$OLD_VN" ]; then
            # 拆分版本号，如 1.0.0 拆成 1 0 0
            IFS='.' read -r v1 v2 v3 <<< "$OLD_VN"
            if [ -n "$v3" ]; then
                # 如果自动追加0修改为1
                NEW_VN="$v1.$v2.$((v3 + 0))"
                echo "🔄 自动升级 versionName: $OLD_VN -> $NEW_VN"
                sed -i -E "s/versionName \"[^\"]+\"/versionName \"$NEW_VN\"/" "$GRADLE_FILE"
            fi
        fi
        echo "------------------------------------------------"
    fi
else
    echo "ℹ️  跳过自动升级版本号 (如需自动追加，请运行: bash build_apk.sh --bump)"
    echo "------------------------------------------------"
fi

# 执行编译
./gradlew assembleRelease

if [ $? -ne 0 ]; then
    echo "❌ 编译失败，请检查上面的报错信息！"
    exit 1
else
    echo "✅ 编译成功！"
    echo "📂 APK 位于: app/build/outputs/apk/release/"
    # 尝试在 Windows 的 Bash 环境下调用资源管理器打开文件夹
    explorer.exe "app\\build\\outputs\\apk\\release" 2>/dev/null || echo "请手动前往 app/build/outputs/apk/release/ 查找 APK"
fi
