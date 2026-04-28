// ========================================
// auth.go — 流代理场景支持 Header 传递 Token
// ========================================

// AuthMiddleware 中关于 Client Token 认证的部分修改如下：

// 尝试 Client Token 认证
token := clientToken  // 优先从 X-Client-Token Header 获取
if token == "" {
    token = strings.TrimPrefix(auth, "Bearer ")  // 其次从 Authorization Header 获取
}
// 流代理场景：兼容旧版本通过 query 参数传递 token（已废弃，建议用 Header）
if token == "" {
    token = c.Query("token")
    if token != "" {
        // ★ 新增：记录废弃警告
        slog.Warn("token 通过 URL query 传递已废弃，请使用 Header",
            "path", c.Request.URL.Path,
            "ip", c.ClientIP(),
        )
    }
}

// ★ 后续验证逻辑不变...
if token != "" && db != nil {
    var clientID int64
    var status string
    err := db.QueryRow(`SELECT id, status FROM clients WHERE access_token=?`, token).Scan(&clientID, &status)
    if err == nil {
        if status != "approved" {
            c.JSON(http.StatusForbidden, gin.H{
                "code":    403,
                "message": "客户端未授权",
            })
            c.Abort()
            return
        }
        c.Set("auth_type", "client")
        c.Set("client_id", clientID)
        c.Set("client_token", token)
        c.Next()
        return
    }
}
