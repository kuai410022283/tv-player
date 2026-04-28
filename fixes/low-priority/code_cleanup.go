// ========================================
// main.go — 删除无意义的 init()，清理未使用 import
// ========================================

// 删除以下代码：
// func init() {
//     _ = strings.Contains
// }

// 同时从 import 中移除未使用的 "strings" 包
// （如果 strings 在其他地方未使用的话）


// ========================================
// main.go — 添加日志轮转（使用 lumberjack）
// ========================================
// go get gopkg.in/natefinsh/lumberjack.v2

import (
    "gopkg.in/natefinsh/lumberjack.v2"
)

func setupLogging() {
    logLevel := slog.LevelInfo
    if os.Getenv("LOG_LEVEL") == "debug" {
        logLevel = slog.LevelDebug
    }

    // ★ 使用 lumberjack 实现日志轮转
    logWriter := &lumberjack.Logger{
        Filename:   "./data/logs/tvplayer.log",
        MaxSize:    50,   // 单文件最大 50MB
        MaxBackups: 3,    // 最多保留 3 个备份
        MaxAge:     30,   // 最多保留 30 天
        Compress:   true, // 压缩旧日志
    }

    // 同时输出到 stdout 和文件
    multiWriter := io.MultiWriter(os.Stdout, logWriter)

    slog.SetDefault(slog.New(slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
        Level: logLevel,
    })))
}
