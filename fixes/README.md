# TV-Player 问题修复汇总

## 修复文件清单

### 🔴 严重问题

| # | 问题 | 修复文件 | 说明 |
|---|------|----------|------|
| 1 | SSRF 漏洞 | `01-ssrf-fix/url_validator.go` | 新增 URL 校验，禁止内网/保留地址 |
| 1 | SSRF 漏洞 | `01-ssrf-fix/stream_service_patch.go` | 流代理 + 健康检查添加校验 |
| 1 | SSRF 漏洞 | `01-ssrf-fix/m3u_importer_patch.go` | M3U 导入添加校验 + 状态码检查 |
| 1 | SSRF 漏洞 | `01-ssrf-fix/channel_service_patch.go` | 频道创建/更新添加校验 |
| 1 | SSRF 漏洞 | `01-ssrf-fix/url_validator_test.go` | 单元测试 |
| 2 | 密码明文存储 | `02-password-hash/password_hash.go` | bcrypt 哈希密码 |
| 2 | 密码明文存储 | `02-password-hash/main_patch.go` | 安全检查适配哈希 |
| 2 | 密码明文存储 | `02-password-hash/cmd/hash-password/main.go` | CLI 哈希生成工具 |
| 3 | 默认凭据 | `03-default-credentials/config_fix.go` | 自动生成 secret，禁止空密码 |

### 🟠 高危问题

| # | 问题 | 修复文件 | 说明 |
|---|------|----------|------|
| 4 | Token URL 泄露 | `04-token-in-url/ApiClient.kt` | 移除 URL 中的 token |
| 4 | Token URL 泄露 | `04-token-in-url/PlayerActivity_patch.kt` | ExoPlayer 使用 Header 传递 token |
| 4 | Token URL 泄露 | `04-token-in-url/auth_patch.go` | 后端兼容 Header + 废弃 query |
| 5 | SQL 拼接 | `05-06-sql-cors/sql_fix.go` | strings.Builder 安全拼接 |
| 6 | CORS 全开 | `05-06-sql-cors/cors_fix.go` | 从配置读取允许来源 |
| 7 | Token 明文存储 | `07-android-token-encrypt/ClientAuthManager.kt` | EncryptedSharedPreferences |
| 7 | Token 明文存储 | `07-android-token-encrypt/build.gradle.patch` | 添加 security-crypto 依赖 |

### 🟡 中等问题

| # | 问题 | 修复文件 | 说明 |
|---|------|----------|------|
| 8 | SQLite 并发 | `08-09-10-db-health-m3u/database_fix.go` | 连接数从 1 提升到 5 |
| 9 | 健康检查全量 | `08-09-10-db-health-m3u/health_check_fix.go` | 限制最大检查数量 |
| 10 | M3U 无状态码 | `08-09-10-db-health-m3u/m3u_importer_fix.go` | 添加 HTTP 状态码检查 |
| 11 | 分组删除外键 | `11-12-13-group-rate-batch/group_delete_fix.go` | 事务中移到"未分类" |
| 12 | 限流器泄漏 | `11-12-13-group-rate-batch/rate_limiter_fix.go` | 添加 maxSize + LRU 淘汰 |
| 13 | 批量无事务 | `11-12-13-group-rate-batch/batch_fix.go` | 包裹数据库事务 |

### 🔵 低危问题

| # | 问题 | 修复文件 | 说明 |
|---|------|----------|------|
| 16 | 无意义 init() | `low-priority/code_cleanup.go` | 删除 + 清理 import |
| 17 | 日志无轮转 | `low-priority/code_cleanup.go` | 使用 lumberjack 轮转 |

## 使用方法

1. 将对应修复文件中的代码复制到项目源文件中
2. 安装新依赖：
   ```bash
   # Go 后端
   go get golang.org/x/crypto/bcrypt
   go get gopkg.in/natefinsh/lumberjack.v2

   # Android
   # 在 build.gradle 中添加: implementation "androidx.security:security-crypto:1.1.0-alpha06"
   ```
3. 生成密码哈希：
   ```bash
   go run cmd/hash-password/main.go "你的密码"
   ```
4. 更新 config.yaml 使用哈希密码
