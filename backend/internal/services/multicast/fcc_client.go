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

// FccType represents the type of FCC protocol
type FccType string

const (
	FccTypeTelecom FccType = "telecom"
	FccTypeHuawei  FccType = "huawei"
)

type FCCClient struct {
	conn       *net.UDPConn
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

	mcastAddr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return nil, err
	}


	var conn *net.UDPConn
	var localPort int
	// Try binding to a port in the configured range
	for port := fccPortStart; port <= fccPortEnd; port++ {
		addr := &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: port}
		conn, err = net.ListenUDP("udp", addr)
		if err == nil {
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
	pk := make([]byte, 24)
	
	// RTCP Header (8 bytes)
	pk[0] = 0x80 | 2 // Version 2, Padding 0, FMT 2 (FCC_FMT_TELECOM_REQ)
	pk[1] = 205      // Type: Generic RTP Feedback (205)
	
	lenWords := uint16(len(pk)/4 - 1)
	binary.BigEndian.PutUint16(pk[2:4], lenWords)

	// SSRC is zero (bytes 4-7)
	// Media source SSRC (bytes 8-11) - multicast IP
	ip4 := c.mcastAddr.IP.To4()
	if ip4 == nil {
		return fmt.Errorf("multicast address must be IPv4")
	}
	copy(pk[8:12], ip4)

	// FCI
	binary.BigEndian.PutUint16(pk[16:18], uint16(localPort))
	binary.BigEndian.PutUint16(pk[18:20], uint16(c.mcastAddr.Port))
	copy(pk[20:24], ip4)

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
		// If it's RTCP (like FMT 3 Response), skip it
		pt := data[1] & 0x7F
		if pt >= 192 && pt <= 223 {
			// RTCP payload, not media, return 0 to skip
			return 0, nil
		}
		
		// If it's RTP, return it
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
			_, _ = c.conn.WriteToUDP(termPk, c.serverAddr)
		}
		return c.conn.Close()
	}
	return nil
}

func (c *FCCClient) handshakeHuawei(localPort int) error {
	pk := make([]byte, 32)
	
	// 1. RTCP Header
	pk[0] = 0x80 | 5 // V=2, P=0, FMT=5
	pk[1] = 205      // PT=205
	binary.BigEndian.PutUint16(pk[2:4], 7) // Length = 7

	// 2. Media Source SSRC (Multicast IP)
	ip4 := c.mcastAddr.IP.To4()
	if ip4 == nil {
		return fmt.Errorf("multicast address must be IPv4")
	}
	copy(pk[8:12], ip4)

	// 3. Local IP
	localIP := net.ParseIP("0.0.0.0").To4()
	if tempConn, err := net.Dial("udp", c.serverAddr.String()); err == nil {
		if udpAddr, ok := tempConn.LocalAddr().(*net.UDPAddr); ok {
			localIP = udpAddr.IP.To4()
		}
		tempConn.Close()
	}
	copy(pk[20:24], localIP)

	// 4. Client Port and Flags
	binary.BigEndian.PutUint16(pk[24:26], uint16(localPort))
	binary.BigEndian.PutUint16(pk[26:28], 0x8000)
	binary.BigEndian.PutUint32(pk[28:32], 0x20000000)

	_, err := c.conn.WriteToUDP(pk, c.serverAddr)
	if err != nil {
		return fmt.Errorf("failed to send FCC huawei request: %w", err)
	}
	
	slog.Debug("FCC Huawei (FMT 5) handshake sent", "localPort", localPort, "server", c.serverAddr.String(), "mcast", c.mcastAddr.String())
	return nil
}
