package api

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// hashPassword 使用 bcrypt 对密码进行哈希
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// checkPassword 验证密码是否匹配哈希
func checkPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// isBcryptHash 判断字符串是否为 bcrypt 哈希格式
func isBcryptHash(s string) bool {
	// bcrypt 哈希格式: $2a$... 或 $2b$... 或 $2y$...，长度 60
	return len(s) == 60 && (s[0:4] == "$2a$" || s[0:4] == "$2b$" || s[0:4] == "$2y$")
}

// InitSecret 设置 JWT 密钥和管理员密码
// ★ 修改：如果密码不是 bcrypt 哈希格式，自动哈希后存储
func InitSecret(secret, password string, expireHours int) {
	jwtSecret = secret
	if expireHours > 0 {
		jwtExpireHours = expireHours
	}

	// 如果密码已经是 bcrypt 哈希，直接使用
	if isBcryptHash(password) {
		adminPassword = password
		return
	}

	// 否则，自动哈希存储
	hashed, err := hashPassword(password)
	if err != nil {
		slog.Error("密码哈希失败，使用明文（不安全！）", "error", err)
		adminPassword = password
		return
	}
	adminPassword = hashed
	slog.Info("管理员密码已自动哈希存储")
}

func getAdminPassword() string {
	if adminPassword != "" {
		return adminPassword
	}
	if p := os.Getenv("ADMIN_PASSWORD"); p != "" {
		return p
	}
	return "admin123"
}

// verifyAdminPassword 验证管理员密码（兼容明文和哈希）
func verifyAdminPassword(input string) bool {
	stored := getAdminPassword()

	// 如果存储的是 bcrypt 哈希
	if isBcryptHash(stored) {
		return checkPassword(input, stored)
	}

	// 兼容旧的明文比较（过渡期使用，后续可移除）
	if input == stored {
		slog.Warn("管理员密码仍在使用明文比较，建议升级为 bcrypt 哈希")
		return true
	}

	return false
}

// ── AdminLogin handler 修改 ──

// AdminLogin 处理管理员登录请求
func (h *Handler) AdminLogin(c *gin.Context) {
	var body struct {
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, 400, "请提供密码")
		return
	}

	// ★ 修改：使用 bcrypt 验证替代明文比较
	if !verifyAdminPassword(body.Password) {
		slog.Warn("admin login failed", "ip", c.ClientIP())
		fail(c, 401, "密码错误")
		return
	}

	token, err := generateAdminToken(getJWTSecret())
	if err != nil {
		failInternal(c, err, "生成令牌失败")
		return
	}

	ok(c, gin.H{"token": token, "message": "登录成功"})
}

// ── 生成哈希密码的辅助函数（用于配置文件预生成） ──

// GeneratePasswordHash 生成 bcrypt 哈希，可用于替换 config.yaml 中的明文密码
func GeneratePasswordHash(password string) (string, error) {
	return hashPassword(password)
}

// ── 辅助函数 ──

func generateAdminToken(secret string) (string, error) {
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "admin",
		"iss":  "tvplayer",
		"iat":  now.Unix(),
		"exp":  now.Add(time.Duration(jwtExpireHours) * time.Hour).Unix(),
	})
	return token.SignedString([]byte(secret))
}

// ── 补充 gin 的 import ──
// （实际使用时需要确保 handler.go 中有正确的 import）
