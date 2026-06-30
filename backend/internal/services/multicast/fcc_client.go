package multicast

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

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
	conn       *net.UDPConn
	signalConn *net.UDPConn
	buffer     *[]byte
	serverAddr *net.UDPAddr
	mcastAddr  *net.UDPAddr
	fccServerIP string
	fccType     FccType
}

func NewFCCClient(ctx context.Context, fccServerIP string, fccPortStart, fccPortEnd int, targetMulticast string, fccType FccType) (*FCCClient, error) {
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
					conn.Close()
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

	client := &FCCClient{
		conn:        conn,
		signalConn:  signalConn,
		serverAddr:  serverAddr,
		mcastAddr:   mcastAddr,
		fccServerIP: fccServerIP,
		buffer:      udpBufferPool.Get().(*[]byte),
		fccType:     fccType,
	}

	if fccType == FccTypeHuawei {
		if err := client.handshakeHuawei(localPort); err != nil {
			client.Close()
			return nil, err
		}
	} else {
		if err := client.handshakeTelecom(localPort); err != nil {
			client.Close()
			return nil, err
		}
	}

	return client, nil
}

func (c *FCCClient) handshakeTelecom(localPort int) error {
	pk := make([]byte, 40)
	
	// RTCP Header (12 bytes)
	pk[0] = 0x80 | 2 // Version 2, Padding 0, FMT 2 (FCC_FMT_TELECOM_REQ)
	pk[1] = 205      // Type: Generic RTP Feedback (205)
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
	_ = c.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, _, err := c.conn.ReadFromUDP(*c.buffer)
	if err != nil {
		return 0, err
	}
	data := (*c.buffer)[:n]

	// Basic RTP check. The FCC server sends RTP or RTCP back.
	if len(data) >= 12 && (data[0]&0xC0) == 0x80 {
		// If it's RTCP (PT >= 192 && PT <= 223)
		pt := data[1] & 0x7F
		if pt >= 192 && pt <= 223 {
			fmtField := data[0] & 0x1F

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
			c.signalConn.Close()
		}
		return c.conn.Close()
	}
	return nil
}

func (c *FCCClient) handshakeHuawei(localPort int) error {
	pk := make([]byte, 28)
	
	// 1. RTCP Header (12 bytes)
	pk[0] = 0x80 | 5 // V=2, P=0, FMT=5
	pk[1] = 205      // PT=205
	binary.BigEndian.PutUint16(pk[2:4], 6) // Length = 6 (28 bytes)

	ip4 := c.mcastAddr.IP.To4()
	if ip4 == nil {
		return fmt.Errorf("multicast address must be IPv4")
	}
	copy(pk[8:12], ip4) // Media Source SSRC (Multicast IP)

	// 2. Payload (16 bytes)
	copy(pk[12:16], ip4) // mcast_ip

	localIP := net.ParseIP("0.0.0.0").To4()
	if tempConn, err := net.Dial("udp", c.serverAddr.String()); err == nil {
		if udpAddr, ok := tempConn.LocalAddr().(*net.UDPAddr); ok {
			localIP = udpAddr.IP.To4()
		}
		tempConn.Close()
	}
	copy(pk[16:20], localIP) // client_ip

	signalPort := localPort + 1
	binary.BigEndian.PutUint16(pk[20:22], uint16(signalPort)) // client_port
	binary.BigEndian.PutUint16(pk[22:24], 0x8000) // flags
	binary.BigEndian.PutUint32(pk[24:28], 0x20000000) // params

	var err error
	if c.signalConn != nil {
		_, err = c.signalConn.WriteToUDP(pk, c.serverAddr)
	} else {
		_, err = c.conn.WriteToUDP(pk, c.serverAddr)
	}
	
	if err != nil {
		return fmt.Errorf("failed to send FCC huawei request: %w", err)
	}
	slog.Debug("FCC Huawei handshake sent", "signalPort", signalPort, "server", c.serverAddr.String(), "mcast", c.mcastAddr.String())
	return nil
}
