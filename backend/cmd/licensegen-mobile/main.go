// Package licensegen 提供 gomobile 兼容的授权码生成 API。
// 通过 gomobile bind 编译为 Android AAR，供 APK 调用。
// 加密逻辑与 license-gen.exe 完全一致。
package licensegen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/license"
)

// Generate generates a formatted license key for the given machine code.
//
// expireDays: 授权天数，0 表示永久授权。
// 返回格式化后的授权码（每 5 位用 "-" 分隔）。
func Generate(machineCode string, expireDays int) (string, error) {
	// 计算过期日期
	var expireDate string
	if expireDays <= 0 {
		expireDate = "permanent"
	} else {
		expireDate = time.Now().AddDate(0, 0, expireDays).Format("2006-01-02")
	}

	return generateWithDate(machineCode, expireDate)
}

// GenerateWithDate generates a formatted license key with a specific expiration date.
//
// expireDate: "2027-12-31" 或 "permanent"。
func GenerateWithDate(machineCode string, expireDate string) (string, error) {
	// 验证日期格式
	if expireDate != "permanent" {
		if _, err := time.Parse("2006-01-02", expireDate); err != nil {
			return "", fmt.Errorf("invalid date format: %s (expected YYYY-MM-DD or 'permanent')", expireDate)
		}
	}
	return generateWithDate(machineCode, expireDate)
}

func generateWithDate(machineCode string, expireDate string) (string, error) {
	// 生成随机序列号（8 字节 → 16 位 hex）
	seqBytes := make([]byte, 8)
	if _, err := rand.Read(seqBytes); err != nil {
		return "", fmt.Errorf("generate seq failed: %w", err)
	}
	seq := hex.EncodeToString(seqBytes)

	// 构建明文: 机器码|过期日期|MP|序列号
	plaintext := fmt.Sprintf("%s|%s|MP|%s", machineCode, expireDate, seq)

	// 加密
	encoded, err := license.EncryptLicense(plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt failed: %w", err)
	}

	// 格式化
	return formatLicenseKey(encoded), nil
}

// formatLicenseKey 格式化授权码，与 license-gen CLI 一致。
// base64url 的 "-" 替换为 "." → 每 5 位加 "-" 分隔。
func formatLicenseKey(key string) string {
	key = strings.ReplaceAll(key, "-", ".")
	var parts []string
	for i := 0; i < len(key); i += 5 {
		end := i + 5
		if end > len(key) {
			end = len(key)
		}
		parts = append(parts, key[i:end])
	}
	return strings.Join(parts, "-")
}