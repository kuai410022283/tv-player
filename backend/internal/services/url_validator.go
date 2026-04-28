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

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "rtmp", "rtsp":
		// OK
	case "file", "ftp", "gopher", "dict", "data":
		return fmt.Errorf("禁止的协议: %s", scheme)
	default:
		return fmt.Errorf("不支持的协议: %s", scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}

	if err := checkNotInternal(host); err != nil {
		return err
	}

	return nil
}

func checkNotInternal(host string) error {
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

	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}

	for _, ip := range ips {
		if isInternalIP(ip) {
			return fmt.Errorf("禁止访问内网地址: %s (%s)", host, ip.String())
		}
	}

	return nil
}

func isInternalIP(ip net.IP) bool {
	if ip.IsLoopback() {
		return true
	}
	if ip.IsLinkLocalUnicast() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	if ip.IsUnspecified() {
		return true
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
