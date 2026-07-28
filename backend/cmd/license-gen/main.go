package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/license"
)

func main() {
	// 解析命令行参数
	machine := flag.String("machine", "", "机器码（必填）")
	expire := flag.String("expire", "", "过期时间，格式: 2027-12-31 / permanent / 30d（必填）")
	genKey := flag.Bool("gen-key", false, "生成密钥（仅用于生成 secret.go 的种子密钥）")
	flag.Parse()

	if *genKey {
		seed := generateSeed()
		fmt.Println("生成的密钥种子（请写入 internal/license/secret.go）：")
		fmt.Println(seed)
		return
	}

	if *machine == "" || *expire == "" {
		fmt.Println("用法: license-gen.exe -machine <机器码> -expire <过期日期>")
		fmt.Println("示例:")
		fmt.Println("  license-gen.exe -machine a3f2c8e1... -expire 2027-12-31")
		fmt.Println("  license-gen.exe -machine a3f2c8e1... -expire permanent")
		fmt.Println("  license-gen.exe -machine a3f2c8e1... -expire 30d")
		fmt.Println("  license-gen.exe -machine a3f2c8e1... -expire 365d")
		os.Exit(1)
	}

	// 处理按天过期
	expireDate := *expire
	if strings.HasSuffix(*expire, "d") {
		daysStr := strings.TrimSuffix(*expire, "d")
		var days int
		if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil || days <= 0 {
			fmt.Println("错误: 按天过期格式无效，示例: 30d, 365d")
			os.Exit(1)
		}
		expireDate = time.Now().AddDate(0, 0, days).Format("2006-01-02")
	}

	// 验证日期格式
	if expireDate != "permanent" {
		if _, err := time.Parse("2006-01-02", expireDate); err != nil {
			fmt.Println("错误: 过期日期格式无效，请使用 YYYY-MM-DD 格式或 permanent")
			os.Exit(1)
		}
	}

	// 生成唯一序列号（8字节随机，hex编码 = 16字符）
	seqBytes := make([]byte, 8)
	if _, err := rand.Read(seqBytes); err != nil {
		fmt.Println("错误: 生成序列号失败:", err)
		os.Exit(1)
	}
	seq := hex.EncodeToString(seqBytes)

	// 构建明文
	plaintext := fmt.Sprintf("%s|%s|%s|%s", *machine, expireDate, "MP", seq)

	// 加密
	encoded, err := license.EncryptLicense(plaintext)
	if err != nil {
		fmt.Println("错误: 加密失败:", err)
		os.Exit(1)
	}

	// 格式化输出：每5位一组加连字符
	formatted := formatLicenseKey(encoded)

	fmt.Println("")
	fmt.Println("┌─ 授权码 ─────────────────────────────────────────┐")
	fmt.Printf("│ %-47s│\n", formatted)
	fmt.Println("└───────────────────────────────────────────────────┘")
	fmt.Println("")
	fmt.Println("  机器码:", *machine)
	fmt.Println("  过期时间:", expireDate)
	fmt.Println("  序列号:", seq)
	fmt.Println("")

	// 生成授权码 TXT 文件
	expireDisplay := expireDate
	if expireDate == "permanent" {
		expireDisplay = "permanent"
	}
	// 文件名含过期时间和机器码前缀，便于识别
	machineShort := *machine
	if len(machineShort) > 8 {
		machineShort = machineShort[:8]
	}
	txtName := fmt.Sprintf("license_%s_%s.txt", expireDisplay, machineShort)
	txtPath := filepath.Join(".", txtName)
	txtContent := fmt.Sprintf(`授权码: %s
机器码: %s
过期时间: %s
序列号: %s
生成时间: %s
`, formatted, *machine, expireDate, seq, time.Now().Format("2006-01-02 15:04:05"))
	_ = os.WriteFile(txtPath, []byte(txtContent), 0644)
	fmt.Println("  ✅ 已生成文件:", txtName)
	fmt.Println("")
	fmt.Println("  授权码已复制到剪贴板（如果支持）")
}

// formatLicenseKey 将 base64url 编码的授权码格式化为每5位一组加连字符。
// 重要：base64url 编码使用 "-" 和 "_" 作为合法字符，而分隔符也是 "-"。
// 为避免冲突，格式化前先将 base64url 中的 "-" 替换为 "."，服务端解析时再还原。
func formatLicenseKey(key string) string {
	// 去掉可能的填充字符
	key = strings.TrimRight(key, "=")
	// base64url 的 "-" 替换为 "."，避免与分隔符冲突
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

// generateSeed 生成一个随机密钥种子
func generateSeed() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return fmt.Sprintf("MPv2.0-%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}
