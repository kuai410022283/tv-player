// Package license 提供 VIP 授权订阅功能。
//
// 本文件由脚本自动生成，请勿手动修改。
// 生成方式: go run cmd/license-gen/main.go -gen-key
// 生成的密钥用于 AES-256-GCM 加解密授权码。

package license

// embeddedSecret 是 AES-256 密钥的种子。
// 使用 PBKDF2 派生为最终的 AES-256 密钥。
// 修改此密钥会导致所有已生成的授权码失效。
//
// 生产部署时通过 ldflags 注入自定义密钥：
//
//	go build -ldflags "-X 'github.com/mediaplayer/backend/internal/license.embeddedSecret=你的密钥'" ./cmd/server/
//
// 默认值仅用于开发环境，生产环境必须替换。
var embeddedSecret = "MPv2.0-dev-default-change-me-in-production"
