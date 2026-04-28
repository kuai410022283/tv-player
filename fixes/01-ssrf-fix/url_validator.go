package services

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateStreamURL 校验流地址，防止 SSRF 攻击
func ValidateStreamURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL 不能为空")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("URL 格式错误: %w", err)
	}

	// 只允许 http/https/rtmp/rtsp 协议
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "rtmp", "rtsp":
		// OK
	case "file", "ftp", "gopher", "dict", "data":
		return fmt.Errorf("禁止的协议: %s", scheme)
	default:
		return fmt.Errorf("不支持的协议: %s", scheme)
	}

	// 解析主机名
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}

	// 禁止内网地址
	if err := checkNotInternal(host); err != nil {
		return err
	}

	return nil
}

// checkNotInternal 检查主机是否为内网/保留地址
func checkNotInternal(host string) error {
	// 先检查常见保留域名
	lower := strings.ToLower(host)
	reservedDomains := []string{
		"localhost",
		"metadata.google.internal",
		"instance-data",
		"169.254.169.254",
	}
	for _, d := range reservedDomains {
		if lower == d || strings.HasSuffix(lower, "."+d) {
			return fmt.Errorf("禁止访问保留域名: %s", host)
		}
	}

	// 解析 IP（支持域名和直接 IP）
	ips, err := net.LookupIP(host)
	if err != nil {
		// DNS 解析失败不一定是安全问题，允许继续（连接时会失败）
		return nil
	}

	for _, ip := range ips {
		if isInternalIP(ip) {
			return fmt.Errorf("禁止访问内网地址: %s (%s)", host, ip.String())
		}
	}

	return nil
}

// isInternalIP 判断 IP 是否为内网/保留地址
func isInternalIP(ip net.IP) bool {
	if ip.IsLoopback() { // 127.0.0.0/8, ::1
		return true
	}
	if ip.IsLinkLocalUnicast() { // 169.254.0.0/16, fe80::/10
		return true
	}
	if ip.IsPrivate() { // 10/8, 172.16/12, 192.168/16, fc00::/7
		return true
	}
	if ip.IsUnspecified() { // 0.0.0.0, ::
		return true
	}
	// 额外检查云元数据服务常用 IP
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
