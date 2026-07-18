package multicast

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// detectPublicIP 通过外部服务自动检测公网IP
func detectPublicIP() (string, error) {
	services := []string{
		"https://api.ipify.org",
		"https://ifconfig.me/ip",
		"https://checkip.amazonaws.com",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 3 * time.Second}

	for _, svc := range services {
		resp, err := client.Get(svc)
		if err != nil {
			slog.Debug("FCC: Failed to detect public IP from", "service", svc, "error", err)
			continue
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			continue
		}

		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			slog.Info("FCC: Auto-detected public IP", "ip", ip, "service", svc)
			return ip, nil
		}
	}

	return "", fmt.Errorf("failed to detect public IP from all services")
}

// resolveIP 解析IP或域名为IP地址
func resolveIP(host string) (string, error) {
	// 如果已经是IP地址，直接返回
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}

	// 解析域名
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve domain %s: %w", host, err)
	}

	if len(ips) == 0 {
		return "", fmt.Errorf("no IP found for domain %s", host)
	}

	return ips[0].String(), nil
}

type FCCRedirectError struct {
	NewIP   string
	NewPort int
}

func (e *FCCRedirectError) Error() string {
	return fmt.Sprintf("fcc redirect to %s:%d", e.NewIP, e.NewPort)
}

type FCCSyncNotification struct{}

func (e *FCCSyncNotification) Error() string {
	return "fcc sync notification"
}

// FccType represents the type of FCC protocol
type FccType string

const (
	FccTypeTelecom FccType = "telecom"
	FccTypeHuawei  FccType = "huawei"
)

type FCCClient struct {
	conn        *net.UDPConn
	signalConn  *net.UDPConn
	buffer      *[]byte
	serverAddr  *net.UDPAddr
	mcastAddr   *net.UDPAddr
	fccServerIP string
	fccType     FccType
	publicIP    string // 公网IP，用于内网服务器

	// 华为协议信号端口读取
	signalChan   chan []byte
	signalCtx    context.Context
	signalCancel context.CancelFunc
}

func NewFCCClient(ctx context.Context, fccServerIP string, fccPortStart, fccPortEnd int, targetMulticast string, fccType FccType, publicIP ...string) (*FCCClient, error) {
	if fccType != FccTypeTelecom && fccType != FccTypeHuawei {
		return nil, fmt.Errorf("unsupported fcc type: %s", fccType)
	}

	serverAddr, err := net.ResolveUDPAddr("udp", fccServerIP)
	if err != nil {
		return nil, err
	}

	addrStr := strings.TrimPrefix(targetMulticast, "udp://")
	addrStr = strings.TrimPrefix(addrStr, "rtp://")
	addrStr = strings.TrimPrefix(addrStr, "@")
	if idx := strings.Index(addrStr, "?"); idx != -1 {
		addrStr = addrStr[:idx]
	}

	mcastAddr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}

	var conn *net.UDPConn
	var signalConn *net.UDPConn
	var localPort int

	// Try binding to a port in the configured range
	for port := fccPortStart; port <= fccPortEnd; port++ {
		// Huawei strictly requires paired ports (media=even, signal=odd=media+1)
		if fccType == FccTypeHuawei && port%2 != 0 {
			continue // Skip odd ports for media
		}

		addr := &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port}
		conn, err = net.ListenUDP("udp", addr)
		if err == nil {
			if fccType == FccTypeHuawei {
				// Try binding the adjacent signal port
				sigAddr := &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port + 1}
				signalConn, err = net.ListenUDP("udp", sigAddr)
				if err != nil {
					_ = conn.Close()
					conn = nil
					continue // Try the next even port
				}
			}
			localPort = port
			break
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("failed to bind any local UDP port in range %d-%d for FCC", fccPortStart, fccPortEnd)
	}

	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	// 处理公网IP参数
	var pubIP string
	if len(publicIP) > 0 && publicIP[0] != "" {
		input := publicIP[0]

		// 支持 "auto" 关键字自动检测公网IP
		if strings.ToLower(input) == "auto" {
			detectedIP, err := detectPublicIP()
			if err != nil {
				slog.Warn("FCC: Failed to auto-detect public IP", "error", err)
			} else {
				pubIP = detectedIP
			}
		} else {
			// 支持域名解析
			resolvedIP, err := resolveIP(input)
			if err != nil {
				slog.Warn("FCC: Failed to resolve public IP", "input", input, "error", err)
			} else {
				pubIP = resolvedIP
			}
		}
	}

	signalCtx, signalCancel := context.WithCancel(ctx)
	client := &FCCClient{
		conn:         conn,
		signalConn:   signalConn,
		serverAddr:   serverAddr,
		mcastAddr:    mcastAddr,
		fccServerIP:  fccServerIP,
		buffer:       udpBufferPool.Get().(*[]byte),
		fccType:      fccType,
		publicIP:     pubIP,
		signalChan:   make(chan []byte, 10),
		signalCtx:    signalCtx,
		signalCancel: signalCancel,
	}

	// 对于华为协议，启动信号端口读取goroutine
	if fccType == FccTypeHuawei && signalConn != nil {
		go client.readSignalPort()
	}

	if fccType == FccTypeHuawei {
		if err := client.handshakeHuawei(localPort); err != nil {
			_ = client.Close()
			return nil, err
		}
	} else {
		if err := client.handshakeTelecom(localPort); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

	return client, nil
}

// readSignalPort 持续从华为协议的信号端口读取数据
func (c *FCCClient) readSignalPort() {
	buf := make([]byte, 2048)
	for {
		select {
		case <-c.signalCtx.Done():
			return
		default:
		}

		_ = c.signalConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := c.signalConn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // 超时继续读取
			}
			return // 其他错误退出
		}

		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case c.signalChan <- data:
			default:
				// channel满，丢弃数据
			}
		}
	}
}

func (c *FCCClient) handshakeTelecom(localPort int) error {
	pk := make([]byte, 40)

	// RTCP Header (12 bytes)
	pk[0] = 0x80 | 2                       // Version 2, Padding 0, FMT 2 (FCC_FMT_TELECOM_REQ)
	pk[1] = 205                            // Type: Generic RTP Feedback (205)
	binary.BigEndian.PutUint16(pk[2:4], 9) // Length (40 bytes -> 9 words)

	ip4 := c.mcastAddr.IP.To4()
	if ip4 == nil {
		return fmt.Errorf("multicast address must be IPv4")
	}
	copy(pk[8:12], ip4) // Media source SSRC (multicast IP)

	// Payload (28 bytes)
	pk[12] = 0x00 // Version
	// pk[13:16] reserved (0x00)
	binary.BigEndian.PutUint16(pk[16:18], uint16(localPort))
	binary.BigEndian.PutUint16(pk[18:20], uint16(c.mcastAddr.Port))
	copy(pk[20:24], ip4) // mcast_ip
	// pk[24:40] stbid (0x00)

	_, err := c.conn.WriteToUDP(pk, c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to send FCC telecom request: %w", err)
	}
	slog.Debug("FCC Telecom handshake sent", "localPort", localPort, "server", c.serverAddr.String(), "mcast", c.mcastAddr.String())
	return nil
}

func (c *FCCClient) Read(p []byte) (int, error) {
	var data []byte

	// 对于华为协议，需要同时从信号端口和媒体端口读取
	// FCC响应（RTCP）发送到信号端口，媒体数据（RTP）发送到媒体端口
	if c.fccType == FccTypeHuawei && c.signalConn != nil {
		// 使用select同时等待信号端口和媒体端口的数据
		type mediaResult struct {
			n   int
			err error
		}
		mediaCh := make(chan mediaResult, 1)

		// 从媒体端口读取
		go func() {
			_ = c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _, err := c.conn.ReadFromUDP(*c.buffer)
			mediaCh <- mediaResult{n: n, err: err}
		}()

		select {
		case signalData := <-c.signalChan:
			// 信号端口有数据（FCC响应）
			data = signalData
			slog.Debug("FCC packet from signal port", "size", len(data), "packet_hex", fmt.Sprintf("%x", data[:min(32, len(data))]))
		case mediaResult := <-mediaCh:
			// 媒体端口有数据
			if mediaResult.err != nil {
				if netErr, ok := mediaResult.err.(net.Error); ok && netErr.Timeout() {
					slog.Debug("FCC Read timeout", "error", mediaResult.err, "localAddr", c.conn.LocalAddr().String(), "serverAddr", c.serverAddr.String())
				}
				return 0, mediaResult.err
			}
			data = (*c.buffer)[:mediaResult.n]
			slog.Debug("FCC packet from media port", "size", mediaResult.n, "packet_hex", fmt.Sprintf("%x", data[:min(32, mediaResult.n)]))
		}
	} else {
		// 电信协议或没有信号端口的情况
		_ = c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := c.conn.ReadFromUDP(*c.buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				slog.Debug("FCC Read timeout", "error", err, "localAddr", c.conn.LocalAddr().String(), "serverAddr", c.serverAddr.String())
			}
			return 0, err
		}
		data = (*c.buffer)[:n]
		slog.Debug("FCC packet received", "size", n, "remoteAddr", remoteAddr.String(), "packet_hex", fmt.Sprintf("%x", data[:min(32, n)]))
	}

	// Basic RTP check. The FCC server sends RTP or RTCP back.
	if len(data) >= 12 && (data[0]&0xC0) == 0x80 {
		// If it's RTCP (PT >= 192 && PT <= 223)
		pt := data[1] & 0x7F
		if pt >= 192 && pt <= 223 {
			fmtField := data[0] & 0x1F
			slog.Debug("FCC RTCP packet", "pt", pt, "fmt", fmtField, "fccType", c.fccType)

			// Telecom Response (FMT=3)
			if c.fccType == FccTypeTelecom && pt == 205 && fmtField == 3 && len(data) >= 36 {
				result := data[12]
				typeField := data[13]

				if result == 0x00 { // Success
					switch typeField {
					case 1:
						return 0, &FCCSyncNotification{} // Join multicast immediately
					case 3:
						// Redirect
						newPort := binary.BigEndian.Uint16(data[14:16])
						newIP := net.IPv4(data[20], data[21], data[22], data[23])
						if newIP != nil && !newIP.IsUnspecified() && newPort != 0 {
							return 0, &FCCRedirectError{NewIP: newIP.String(), NewPort: int(newPort)}
						}
					case 2:
						mediaPort := binary.BigEndian.Uint16(data[16:18])
						serverIP := net.IPv4(data[20], data[21], data[22], data[23])
						if mediaPort != 0 && serverIP != nil {
							holePunchAddr := &net.UDPAddr{IP: serverIP, Port: int(mediaPort)}
							// Telecom NAT hole punch: 1-byte 0x00
							_, _ = c.conn.WriteToUDP([]byte{0}, holePunchAddr)
							slog.Debug("Punched Telecom media hole", "target", holePunchAddr.String())
						}
					}
				}
			}

			// Huawei Response (FMT=6)
			if c.fccType == FccTypeHuawei && pt == 205 && fmtField == 6 && len(data) >= 36 {
				result := data[12]
				typeField := binary.BigEndian.Uint16(data[14:16])

				if result == 0x01 { // Success
					switch typeField {
					case 1:
						return 0, &FCCSyncNotification{} // Join multicast immediately
					case 3:
						// Redirect
						newPort := binary.BigEndian.Uint16(data[26:28])
						newIP := net.IPv4(data[32], data[33], data[34], data[35])
						if newIP != nil && !newIP.IsUnspecified() && newPort != 0 {
							return 0, &FCCRedirectError{NewIP: newIP.String(), NewPort: int(newPort)}
						}
					case 2:
						serverPort := binary.BigEndian.Uint16(data[26:28])
						serverIP := net.IPv4(data[32], data[33], data[34], data[35])
						if serverPort != 0 && serverIP != nil {
							holePunchAddr := &net.UDPAddr{IP: serverIP, Port: int(serverPort)}
							// Huawei NAT hole punch: 4-byte 0x00 0x03 0x00 0x00
							if c.signalConn != nil {
								_, _ = c.signalConn.WriteToUDP([]byte{0x00, 0x03, 0x00, 0x00}, holePunchAddr)
							} else {
								_, _ = c.conn.WriteToUDP([]byte{0x00, 0x03, 0x00, 0x00}, holePunchAddr)
							}
							slog.Debug("Punched Huawei media hole", "target", holePunchAddr.String())
						}
					}
				}
			}

			// Huawei Sync Notification (FMT=8)
			if c.fccType == FccTypeHuawei && pt == 205 && fmtField == 8 {
				slog.Debug("FCC Huawei Sync Notification Received (FMT=8)")
				return 0, &FCCSyncNotification{}
			}

			// It is an RTCP payload, not a media frame, skip
			return 0, nil
		}

		// If it's RTP, copy and return
		copied := copy(p, data)
		return copied, nil
	}

	return copy(p, data), nil
}

func (c *FCCClient) Close() error {
	// 停止信号端口读取goroutine
	if c.signalCancel != nil {
		c.signalCancel()
	}

	if c.buffer != nil {
		udpBufferPool.Put(c.buffer)
		c.buffer = nil
	}
	if c.conn != nil {
		if c.fccType == FccTypeHuawei && c.fccServerIP != "" {
			termPk := make([]byte, 16)
			termPk[0] = 0x80 | 9 // V=2, FMT=9
			termPk[1] = 205
			binary.BigEndian.PutUint16(termPk[2:4], 3) // Length=3
			ip4 := c.mcastAddr.IP.To4()
			if ip4 != nil {
				copy(termPk[8:12], ip4)
			}
			termPk[12] = 0x01 // Status: joined multicast successfully
			// Send termination from the signal port
			if c.signalConn != nil {
				_, _ = c.signalConn.WriteToUDP(termPk, c.serverAddr)
			} else {
				_, _ = c.conn.WriteToUDP(termPk, c.serverAddr)
			}
		} else if c.fccType == FccTypeTelecom && c.fccServerIP != "" {
			termPk := make([]byte, 16)
			termPk[0] = 0x80 | 5 // V=2, FMT=5 (Telecom Terminate)
			termPk[1] = 205
			binary.BigEndian.PutUint16(termPk[2:4], 3) // Length=3
			ip4 := c.mcastAddr.IP.To4()
			if ip4 != nil {
				copy(termPk[8:12], ip4)
			}
			termPk[12] = 0x01 // type: 0x01 (Telecom Termination sub-type)
			_, _ = c.conn.WriteToUDP(termPk, c.serverAddr)
			slog.Debug("Sent FCC Telecom Terminate packet")
		}
		if c.signalConn != nil {
			_ = c.signalConn.Close()
		}
		return c.conn.Close()
	}
	return nil
}

func (c *FCCClient) handshakeHuawei(localPort int) error {
	pk := make([]byte, 32)

	// 1. RTCP Header (12 bytes)
	pk[0] = 0x80 | 5                       // V=2, P=0, FMT=5
	pk[1] = 205                            // PT=205
	binary.BigEndian.PutUint16(pk[2:4], 7) // Length = 7 (32 bytes)
	// pk[4-7]: Sender SSRC = 0 (already zeroed by make)

	ip4 := c.mcastAddr.IP.To4()
	if ip4 == nil {
		return fmt.Errorf("multicast address must be IPv4")
	}
	copy(pk[8:12], ip4) // Media Source SSRC (Multicast IP)

	// 2. FCI - Feedback Control Information (16 bytes)
	// pk[12-19]: Reserved (8 bytes, already zeroed by make)
	// pk[20-23]: client_ip
	// 优先使用配置的公网IP，否则自动检测本地IP
	var localIP net.IP
	if c.publicIP != "" {
		localIP = net.ParseIP(c.publicIP).To4()
		slog.Info("FCC Huawei: Using configured public IP", "publicIP", c.publicIP)
	}

	if localIP == nil {
		localIP = net.ParseIP("0.0.0.0").To4()
		if tempConn, err := net.Dial("udp", c.serverAddr.String()); err == nil {
			if udpAddr, ok := tempConn.LocalAddr().(*net.UDPAddr); ok {
				localIP = udpAddr.IP.To4()
			}
			_ = tempConn.Close()
		} else {
			slog.Warn("FCC Huawei: Failed to detect local IP via Dial", "error", err, "server", c.serverAddr.String())
		}

		// 如果本地IP检测失败，尝试从conn获取
		if localIP.Equal(net.ParseIP("0.0.0.0").To4()) && c.conn != nil {
			if localAddr, ok := c.conn.LocalAddr().(*net.UDPAddr); ok {
				localIP = localAddr.IP.To4()
			}
		}
		slog.Info("FCC Huawei: Local IP detected", "localIP", localIP.String(), "localPort", localPort, "server", c.serverAddr.String())
	}
	copy(pk[20:24], localIP) // client_ip

	// pk[24-25]: signal_port
	signalPort := localPort + 1
	binary.BigEndian.PutUint16(pk[24:26], uint16(signalPort)) // signal_port

	// pk[26-27]: flag = 0x8000
	binary.BigEndian.PutUint16(pk[26:28], 0x8000) // flag

	// pk[28-31]: redirect support = 0x20000000
	binary.BigEndian.PutUint32(pk[28:32], 0x20000000) // redirect support

	// 记录发送的请求包详情
	slog.Debug("FCC Huawei request packet",
		"packet_hex", fmt.Sprintf("%x", pk),
		"localPort", localPort,
		"signalPort", signalPort,
		"localIP", localIP.String(),
		"mcastIP", ip4.String(),
		"serverAddr", c.serverAddr.String())

	var err error
	if c.signalConn != nil {
		_, err = c.signalConn.WriteToUDP(pk, c.serverAddr)
	} else {
		_, err = c.conn.WriteToUDP(pk, c.serverAddr)
	}

	if err != nil {
		return fmt.Errorf("failed to send FCC huawei request: %w", err)
	}
	slog.Info("FCC Huawei handshake sent", "signalPort", signalPort, "server", c.serverAddr.String(), "mcast", c.mcastAddr.String())
	return nil
}
