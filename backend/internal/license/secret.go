// Package license 提供 VIP 授权订阅功能。
//
// 本文件由脚本自动生成，请勿手动修改。
// 生成方式: go run cmd/license-gen/main.go -gen-key
// 生成的密钥用于 AES-256-GCM 加解密授权码。

package license

// embeddedSecret 是 AES-256 密钥的种子，编译时注入。
// 使用 PBKDF2 派生为最终的 AES-256 密钥。
// 修改此密钥会导致所有已生成的授权码失效。
const embeddedSecret = "MPv2.0-a3f8c2e1-4b7d-9e5f-1a2c3d4e5f6a-2026"