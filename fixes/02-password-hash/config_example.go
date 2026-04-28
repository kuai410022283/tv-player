// ========================================
// config.go — 增加配置校验说明
// ========================================

// AuthConfig 推荐配置示例:
//
// auth:
//   secret: "使用 openssl rand -hex 32 生成的随机字符串"  # 必须修改
//   expire_hours: 720
//   admin_password: "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"  # bcrypt 哈希
//
// 生成哈希: go run cmd/hash-password/main.go "你的密码"
