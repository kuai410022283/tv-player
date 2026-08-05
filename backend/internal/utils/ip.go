package utils

import (
	"net"
	"net/netip"
	"strings"

	"github.com/gin-gonic/gin"
)

// GetRealClientIP 获取真实的客户端 IP，解决反代/Docker 部署时返回私有 IP 的问题。
//
// 策略：
//  1. 优先使用 Gin 的 c.ClientIP()（已处理 trusted_proxies 配置）
//  2. 如果结果是私有/回环地址，且存在 X-Forwarded-For 头，则取最左侧的原始客户端 IP
//  3. 如果 X-Forwarded-For 不存在，则尝试 X-Real-IP 头
//  4. 兜底返回 c.ClientIP() 的原始值
func GetRealClientIP(c *gin.Context) string {
	ip := c.ClientIP()

	// 如果已经是公网 IP，直接返回
	if isPublicIP(ip) {
		return ip
	}

	// 私有/回环地址：尝试从 X-Forwarded-For 获取真实 IP
	// X-Forwarded-For 格式: client, proxy1, proxy2, ...
	// 最左侧始终是原始客户端 IP
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		firstIP := strings.TrimSpace(parts[0])
		if firstIP != "" {
			return firstIP
		}
	}

	// 尝试 X-Real-IP（即使私有也比 c.ClientIP() 的 Docker 网关 IP 更准确）
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		if trimmed := strings.TrimSpace(xri); trimmed != "" {
			return trimmed
		}
	}

	return ip
}

// isPublicIP 判断是否为公网 IP（非私有、非回环、非链路本地、非 IPv6 ULA）
func isPublicIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	// IPv4
	if ip4 := ip.To4(); ip4 != nil {
		return !isPrivateIPv4(ip4)
	}

	// IPv6：回环、链路本地、ULA 视为私有
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if addr, ok := netip.AddrFromSlice(ip); ok {
		if addr.Is6() && addr.As16()[0] == 0xfc || addr.As16()[0] == 0xfd {
			return false // fc00::/7 ULA
		}
	}
	return true
}

// isPrivateIPv4 判断是否为 RFC 1918 私有地址或回环地址
func isPrivateIPv4(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() || // 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
		ip.IsLinkLocalUnicast() // 169.254.0.0/16
}
