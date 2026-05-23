#!/bin/bash

echo "🚀 开始编译 Android TV 客户端 Debug APK..."

# 修复 JAVA_HOME 指向错误的问题（去掉末尾的 \bin）
export JAVA_HOME="D:/Program Files/Android/Android Studio/jbr"

# 确保 gradlew 具有可执行权限
chmod +x ./gradlew

# 执行编译
./gradlew assembleDebug

if [ $? -ne 0 ]; then
    echo "❌ 编译失败，请检查上面的报错信息！"
    exit 1
else
    echo "✅ 编译成功！"
    echo "📂 APK 位于: app/build/outputs/apk/debug/"
    # 尝试在 Windows 的 Bash 环境下调用资源管理器打开文件夹
    explorer.exe "app\\build\\outputs\\apk\\debug" 2>/dev/null || echo "请手动前往 app/build/outputs/apk/debug/ 查找 APK"
fi
