package services

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// AllowLocalFile 是否允许本地文件路径（可通过配置动态开关，默认关闭）
var AllowLocalFile = false

// ValidateStreamURL 校验流地址，防止 SSRF 攻击
func ValidateStreamURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL 不能为空")
	}

	// 检查是否为本地文件路径（以 / 开头，或 Windows 盘符如 C:\）
	if isLocalPath(rawURL) {
		if !AllowLocalFile {
			return fmt.Errorf("本地文件路径未启用，请在设置中开启「允许本地文件」")
		}
		return validateLocalPath(rawURL)
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("URL 格式错误: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "http", "https", "rtmp", "rtsp", "rtp", "udp", "p2p":
		// OK - 网络协议
	case "file":
		// file:// 协议
		if !AllowLocalFile {
			return fmt.Errorf("本地文件路径未启用，请在设置中开启「允许本地文件」")
		}
		return validateLocalPath(u.Path)
	case "ftp", "ftps":
		// FTP 协议，允许但需要主机名验证
		if u.Hostname() == "" {
			return fmt.Errorf("FTP URL 缺少主机名")
		}
		return checkNotInternal(u.Hostname())
	case "webdav", "davs", "smb":
		// WebDAV/SMB 协议，允许但需要主机名验证
		if u.Hostname() == "" {
			return fmt.Errorf("%s URL 缺少主机名", scheme)
		}
		return checkNotInternal(u.Hostname())
	case "gopher", "dict", "data":
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

// isLocalPath 判断是否为本地路径
func isLocalPath(path string) bool {
	// Unix/Linux/Mac: / 开头
	if strings.HasPrefix(path, "/") {
		return true
	}
	// Windows: C:\ 或 C:/ 开头
	if len(path) >= 2 && path[1] == ':' && ((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z')) {
		return true
	}
	// UNC 路径: \\server\share
	if strings.HasPrefix(path, "\\\\") {
		return true
	}
	return false
}

// validateLocalPath 验证本地文件路径（仅做安全检查，不限制目录）
func validateLocalPath(path string) error {
	// 清理路径，防止路径遍历攻击
	cleanPath := filepath.Clean(path)

	// 检查路径遍历（不允许 ..）
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("路径不能包含 '..'，存在安全风险")
	}

	// 基本格式检查
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" {
		return fmt.Errorf("无效的文件路径")
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
	// 允许使用局域网 IP（IPTV 常常位于内网，如 192.168.x.x 或 10.x.x.x）
	// if ip.IsPrivate() {
	// 	return true
	// }
	if ip.IsUnspecified() {
		return true
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
