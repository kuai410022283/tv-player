// ========================================
// cmd/hash-password/main.go
// 密码哈希生成工具，用于替换 config.yaml 中的明文密码
// ========================================
package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: hash-password <密码>")
		fmt.Println("示例: hash-password mySecurePass123")
		os.Exit(1)
	}

	password := os.Args[1]
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成哈希失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("═══════════════════════════════════════════")
	fmt.Println("密码哈希生成成功！")
	fmt.Println("═══════════════════════════════════════════")
	fmt.Printf("\n将以下内容复制到 config.yaml 的 auth.admin_password 字段:\n\n")
	fmt.Printf("  admin_password: \"%s\"\n\n", string(hash))
	fmt.Println("⚠️  请妥善保管原始密码，哈希不可逆。")
}
