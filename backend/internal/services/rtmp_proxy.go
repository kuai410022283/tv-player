package services

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

// serveRtmpProxy 连接 rtmp:// 直播源，解封装为 HTTP-FLV 实时流吐给 ResponseWriter
func (sp *StreamProxy) serveRtmpProxy(channelID int64, _ int64, _ string, _ string, w http.ResponseWriter, r *http.Request, _ *models.Channel, targetURL string) error {
	slog.Info("开始 RTMP 代理", "channel_id", channelID, "target_url", targetURL)

	rtmpURL, err := parseRtmpURL(targetURL)
	if err != nil {
		return fmt.Errorf("解析 RTMP URL 失败: %w", err)
	}

	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
	}
	conn, err := dialer.DialContext(r.Context(), "tcp", rtmpURL.HostPort)
	if err != nil {
		return fmt.Errorf("连接 RTMP 服务器失败 (%s): %w", rtmpURL.HostPort, err)
	}
	defer func() { _ = conn.Close() }()

	// 1. RTMP 握手 (C0+C1 -> S0+S1+S2 -> C2)
	if err := performRtmpHandshake(conn); err != nil {
		return fmt.Errorf("RTMP 握手失败: %w", err)
	}

	// 2. RTMP 信令交互 (connect -> createStream -> play)
	client := newRtmpClient(conn, rtmpURL)
	if err := client.connectApp(); err != nil {
		return fmt.Errorf("RTMP connect 失败: %w", err)
	}
	streamID, err := client.createStream()
	if err != nil {
		return fmt.Errorf("RTMP createStream 失败: %w", err)
	}
	if err := client.playStream(streamID); err != nil {
		return fmt.Errorf("RTMP play 失败: %w", err)
	}

	// 3. 准备 HTTP-FLV 响应头
	w.Header().Set("Content-Type", "video/x-flv")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	// 4. 写入 FLV Header (9 字节: 'F','L','V', ver=1, flag=0x05[audio+video], header_len=9)
	flvHeader := []byte{'F', 'L', 'V', 0x01, 0x05, 0x00, 0x00, 0x00, 0x09, 0x00, 0x00, 0x00, 0x00}
	if _, err := w.Write(flvHeader); err != nil {
		return fmt.Errorf("写入 FLV Header 失败: %w", err)
	}
	if flusher != nil {
		flusher.Flush()
	}

	// 5. 循环读取 RTMP 数据包并组装写出 FLV Tag
	ctx := r.Context()
	lastFlush := time.Now()
	bytesSinceFlush := 0
	flushThreshold := 128 * 1024 // 128KB 缓冲阀值

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		msg, err := client.readMessage()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				slog.Info("RTMP 流读取完毕或连接关闭")
				return nil
			}
			return fmt.Errorf("读取 RTMP 消息失败: %w", err)
		}

		// 8: Audio, 9: Video, 18: Script Data (AMF0 MetaData)
		if msg.TypeId == 8 || msg.TypeId == 9 || msg.TypeId == 18 {
			tagLen := len(msg.Payload)
			tagHeader := make([]byte, 11)
			tagHeader[0] = msg.TypeId
			// DataSize (3 bytes)
			tagHeader[1] = byte(tagLen >> 16)
			tagHeader[2] = byte(tagLen >> 8)
			tagHeader[3] = byte(tagLen)
			// Timestamp (3 bytes)
			tagHeader[4] = byte(msg.Timestamp >> 16)
			tagHeader[5] = byte(msg.Timestamp >> 8)
			tagHeader[6] = byte(msg.Timestamp)
			// TimestampExtended (1 byte)
			tagHeader[7] = byte(msg.Timestamp >> 24)
			// StreamID (3 bytes zero)
			tagHeader[8] = 0
			tagHeader[9] = 0
			tagHeader[10] = 0

			// PreviousTagSize (4 bytes = 11 + tagLen)
			prevTagSize := make([]byte, 4)
			totalTagSize := uint32(11 + tagLen)
			binary.BigEndian.PutUint32(prevTagSize, totalTagSize)

			if _, err := w.Write(tagHeader); err != nil {
				return err
			}
			if _, err := w.Write(msg.Payload); err != nil {
				return err
			}
			if _, err := w.Write(prevTagSize); err != nil {
				return err
			}

			bytesSinceFlush += len(tagHeader) + tagLen + 4
			if flusher != nil && (bytesSinceFlush >= flushThreshold || time.Since(lastFlush) > 200*time.Millisecond) {
				flusher.Flush()
				lastFlush = time.Now()
				bytesSinceFlush = 0
			}
		}
	}
}

type rtmpURLInfo struct {
	HostPort   string
	App        string
	StreamName string
	TcURL      string
}

func parseRtmpURL(rawURL string) (*rtmpURLInfo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(u.Scheme, "rtmp") {
		return nil, fmt.Errorf("非 RTMP 协议: %s", u.Scheme)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "1935"
	}
	hostPort := net.JoinHostPort(host, port)

	path := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		return nil, fmt.Errorf("路径缺失 app/streamName: %s", u.Path)
	}

	app := parts[0]
	streamName := ""
	if len(parts) > 1 {
		streamName = strings.Join(parts[1:], "/")
	}

	tcURL := fmt.Sprintf("rtmp://%s/%s", hostPort, app)
	if u.RawQuery != "" {
		if streamName != "" {
			streamName += "?" + u.RawQuery
		} else {
			app += "?" + u.RawQuery
		}
	}

	return &rtmpURLInfo{
		HostPort:   hostPort,
		App:        app,
		StreamName: streamName,
		TcURL:      tcURL,
	}, nil
}

func performRtmpHandshake(conn net.Conn) error {
	c0c1 := make([]byte, 1537)
	c0c1[0] = 0x03 // RTMP Version 3
	binary.BigEndian.PutUint32(c0c1[1:5], uint32(time.Now().Unix()))

	if _, err := conn.Write(c0c1); err != nil {
		return err
	}

	s0s1s2 := make([]byte, 1+1536+1536)
	if _, err := io.ReadFull(conn, s0s1s2); err != nil {
		return err
	}
	if s0s1s2[0] != 0x03 {
		return fmt.Errorf("不支持的 RTMP 版本: %d", s0s1s2[0])
	}

	c2 := make([]byte, 1536)
	copy(c2, s0s1s2[1:1537])
	if _, err := conn.Write(c2); err != nil {
		return err
	}

	return nil
}

type rtmpMessage struct {
	CsId      uint32
	TypeId    byte
	StreamId  uint32
	Timestamp uint32
	Payload   []byte
}

type rtmpHeaderState struct {
	csId            uint32
	msgTypeId       byte
	msgStreamId     uint32
	msgLength       uint32
	timestamp       uint32
	timestampDelta  uint32
	hasExtTimestamp bool
}

type rtmpClient struct {
	conn         net.Conn
	urlInfo      *rtmpURLInfo
	inChunkSize  uint32
	outChunkSize uint32
	headers      map[uint32]*rtmpHeaderState
}

func newRtmpClient(conn net.Conn, info *rtmpURLInfo) *rtmpClient {
	return &rtmpClient{
		conn:         conn,
		urlInfo:      info,
		inChunkSize:  128,
		outChunkSize: 128,
		headers:      make(map[uint32]*rtmpHeaderState),
	}
}

func (c *rtmpClient) connectApp() error {
	var buf bytes.Buffer
	amfEncodeString(&buf, "connect")
	amfEncodeNumber(&buf, 1.0)

	// Command Object
	amfEncodeObjectHeader(&buf)
	amfEncodeObjectProperty(&buf, "app", c.urlInfo.App)
	amfEncodeObjectProperty(&buf, "flashVer", "FMLE/3.0 (compatible; FMSc/1.0)")
	amfEncodeObjectProperty(&buf, "tcUrl", c.urlInfo.TcURL)
	amfEncodeObjectProperty(&buf, "fpad", false)
	amfEncodeObjectProperty(&buf, "capabilities", 15.0)
	amfEncodeObjectProperty(&buf, "audioCodecs", 3191.0)
	amfEncodeObjectProperty(&buf, "videoCodecs", 252.0)
	amfEncodeObjectProperty(&buf, "videoFunction", 1.0)
	amfEncodeObjectEnd(&buf)

	if err := c.writeChunk(3, 20, 0, 0, buf.Bytes()); err != nil {
		return err
	}

	// 循环接收直至得到 _result
	for {
		msg, err := c.readMessage()
		if err != nil {
			return err
		}
		if msg.TypeId == 20 || msg.TypeId == 17 {
			cmdName, transactionID, _ := amfDecodeCommand(msg.Payload)
			if cmdName == "_result" && transactionID == 1.0 {
				return nil
			}
		}
	}
}

func (c *rtmpClient) createStream() (uint32, error) {
	var buf bytes.Buffer
	amfEncodeString(&buf, "createStream")
	amfEncodeNumber(&buf, 2.0)
	amfEncodeNull(&buf)

	if err := c.writeChunk(3, 20, 0, 0, buf.Bytes()); err != nil {
		return 0, err
	}

	for {
		msg, err := c.readMessage()
		if err != nil {
			return 0, err
		}
		if msg.TypeId == 20 || msg.TypeId == 17 {
			cmdName, transactionID, val := amfDecodeCommand(msg.Payload)
			if cmdName == "_result" && transactionID == 2.0 {
				if num, ok := val.(float64); ok {
					return uint32(num), nil
				}
				return 1, nil
			}
		}
	}
}

func (c *rtmpClient) playStream(streamID uint32) error {
	var buf bytes.Buffer
	amfEncodeString(&buf, "play")
	amfEncodeNumber(&buf, 3.0)
	amfEncodeNull(&buf)
	amfEncodeString(&buf, c.urlInfo.StreamName)
	amfEncodeNumber(&buf, -2000.0) // Start live stream

	return c.writeChunk(8, 20, streamID, 0, buf.Bytes())
}

func (c *rtmpClient) writeChunk(csId uint32, typeId byte, streamId uint32, timestamp uint32, data []byte) error {
	chunkSize := c.outChunkSize
	totalLen := uint32(len(data))
	written := uint32(0)

	for written < totalLen {
		var header []byte
		if written == 0 {
			// Basic Header (fmt=0, csid)
			header = append(header, byte(0x00|(csId&0x3f)))
			// Message Header fmt 0 (11 bytes)
			tsBuf := make([]byte, 3)
			tsVal := timestamp
			if tsVal >= 0xffffff {
				tsVal = 0xffffff
			}
			tsBuf[0] = byte(tsVal >> 16)
			tsBuf[1] = byte(tsVal >> 8)
			tsBuf[2] = byte(tsVal)
			header = append(header, tsBuf...)

			lenBuf := make([]byte, 3)
			lenBuf[0] = byte(totalLen >> 16)
			lenBuf[1] = byte(totalLen >> 8)
			lenBuf[2] = byte(totalLen)
			header = append(header, lenBuf...)

			header = append(header, typeId)

			sidBuf := make([]byte, 4)
			binary.LittleEndian.PutUint32(sidBuf, streamId)
			header = append(header, sidBuf...)

			if timestamp >= 0xffffff {
				extTs := make([]byte, 4)
				binary.BigEndian.PutUint32(extTs, timestamp)
				header = append(header, extTs...)
			}
		} else {
			// Basic Header (fmt=3, csid)
			header = append(header, byte(0xc0|(csId&0x3f)))
		}

		toWrite := totalLen - written
		if toWrite > chunkSize {
			toWrite = chunkSize
		}

		if _, err := c.conn.Write(header); err != nil {
			return err
		}
		if _, err := c.conn.Write(data[written : written+toWrite]); err != nil {
			return err
		}
		written += toWrite
	}
	return nil
}

func (c *rtmpClient) readMessage() (*rtmpMessage, error) {
	type messageBuffer struct {
		payload []byte
	}
	msgBuffers := make(map[uint32]*messageBuffer)

	for {
		// 1. Basic Header
		b := make([]byte, 1)
		if _, err := io.ReadFull(c.conn, b); err != nil {
			return nil, err
		}
		fmtType := (b[0] & 0xc0) >> 6
		csId := uint32(b[0] & 0x3f)

		switch csId {
		case 0:
			b2 := make([]byte, 1)
			if _, err := io.ReadFull(c.conn, b2); err != nil {
				return nil, err
			}
			csId = uint32(b2[0]) + 64
		case 1:
			b23 := make([]byte, 2)
			if _, err := io.ReadFull(c.conn, b23); err != nil {
				return nil, err
			}
			csId = uint32(b23[0]) + uint32(b23[1])*256 + 64
		}

		state, exists := c.headers[csId]
		if !exists {
			state = &rtmpHeaderState{csId: csId}
			c.headers[csId] = state
		}

		// 2. Message Header
		switch fmtType {
		case 0:
			hdr := make([]byte, 11)
			if _, err := io.ReadFull(c.conn, hdr); err != nil {
				return nil, err
			}
			ts := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			state.msgLength = uint32(hdr[3])<<16 | uint32(hdr[4])<<8 | uint32(hdr[5])
			state.msgTypeId = hdr[6]
			state.msgStreamId = binary.LittleEndian.Uint32(hdr[7:11])
			if ts == 0xffffff {
				state.hasExtTimestamp = true
			} else {
				state.timestamp = ts
				state.hasExtTimestamp = false
			}
		case 1:
			hdr := make([]byte, 7)
			if _, err := io.ReadFull(c.conn, hdr); err != nil {
				return nil, err
			}
			tsDelta := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			state.msgLength = uint32(hdr[3])<<16 | uint32(hdr[4])<<8 | uint32(hdr[5])
			state.msgTypeId = hdr[6]
			if tsDelta == 0xffffff {
				state.hasExtTimestamp = true
			} else {
				state.timestampDelta = tsDelta
				state.timestamp += tsDelta
				state.hasExtTimestamp = false
			}
		case 2:
			hdr := make([]byte, 3)
			if _, err := io.ReadFull(c.conn, hdr); err != nil {
				return nil, err
			}
			tsDelta := uint32(hdr[0])<<16 | uint32(hdr[1])<<8 | uint32(hdr[2])
			if tsDelta == 0xffffff {
				state.hasExtTimestamp = true
			} else {
				state.timestampDelta = tsDelta
				state.timestamp += tsDelta
				state.hasExtTimestamp = false
			}
		case 3:
			if state.hasExtTimestamp {
				// retain previous extended timestamp logic
			} else {
				state.timestamp += state.timestampDelta
			}
		}

		if state.hasExtTimestamp {
			extBuf := make([]byte, 4)
			if _, err := io.ReadFull(c.conn, extBuf); err != nil {
				return nil, err
			}
			extTs := binary.BigEndian.Uint32(extBuf)
			if fmtType == 0 {
				state.timestamp = extTs
			} else {
				state.timestamp += extTs
			}
		}

		buf, ok := msgBuffers[csId]
		if !ok {
			buf = &messageBuffer{}
			msgBuffers[csId] = buf
		}

		needed := state.msgLength - uint32(len(buf.payload))
		chunkToRead := needed
		if chunkToRead > c.inChunkSize {
			chunkToRead = c.inChunkSize
		}

		chunkBuf := make([]byte, chunkToRead)
		if _, err := io.ReadFull(c.conn, chunkBuf); err != nil {
			return nil, err
		}
		buf.payload = append(buf.payload, chunkBuf...)

		if uint32(len(buf.payload)) >= state.msgLength {
			msg := &rtmpMessage{
				CsId:      csId,
				TypeId:    state.msgTypeId,
				StreamId:  state.msgStreamId,
				Timestamp: state.timestamp,
				Payload:   buf.payload[:state.msgLength],
			}
			delete(msgBuffers, csId)

			// 内部控制协议处理
			if msg.TypeId == 1 && len(msg.Payload) >= 4 { // Set Chunk Size
				newSize := binary.BigEndian.Uint32(msg.Payload[:4])
				if newSize > 0 && newSize <= 16777216 {
					c.inChunkSize = newSize
				}
			}

			return msg, nil
		}
	}
}

// AMF0 Encoders & Decoders
func amfEncodeString(buf *bytes.Buffer, s string) {
	buf.WriteByte(0x02) // String marker
	length := uint16(len(s))
	_ = binary.Write(buf, binary.BigEndian, length)
	buf.WriteString(s)
}

func amfEncodeNumber(buf *bytes.Buffer, num float64) {
	buf.WriteByte(0x00) // Number marker
	bits := math.Float64bits(num)
	_ = binary.Write(buf, binary.BigEndian, bits)
}

func amfEncodeNull(buf *bytes.Buffer) {
	buf.WriteByte(0x05) // Null marker
}

func amfEncodeObjectHeader(buf *bytes.Buffer) {
	buf.WriteByte(0x03) // Object marker
}

func amfEncodeObjectProperty(buf *bytes.Buffer, key string, val interface{}) {
	length := uint16(len(key))
	_ = binary.Write(buf, binary.BigEndian, length)
	buf.WriteString(key)
	switch v := val.(type) {
	case string:
		amfEncodeString(buf, v)
	case float64:
		amfEncodeNumber(buf, v)
	case bool:
		buf.WriteByte(0x01)
		if v {
			buf.WriteByte(0x01)
		} else {
			buf.WriteByte(0x00)
		}
	}
}

func amfEncodeObjectEnd(buf *bytes.Buffer) {
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x09) // Object end marker
}

func amfDecodeCommand(data []byte) (cmdName string, transactionID float64, extraVal interface{}) {
	r := bytes.NewReader(data)
	cmdName, _ = amfReadString(r)
	transactionID, _ = amfReadNumber(r)
	if r.Len() > 0 {
		extraVal = amfReadAny(r)
	}
	return
}

func amfReadString(r *bytes.Reader) (string, error) {
	marker, err := r.ReadByte()
	if err != nil || marker != 0x02 {
		return "", fmt.Errorf("invalid string marker")
	}
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	strBytes := make([]byte, length)
	if _, err := io.ReadFull(r, strBytes); err != nil {
		return "", err
	}
	return string(strBytes), nil
}

func amfReadNumber(r *bytes.Reader) (float64, error) {
	marker, err := r.ReadByte()
	if err != nil || marker != 0x00 {
		return 0, fmt.Errorf("invalid number marker")
	}
	var bits uint64
	if err := binary.Read(r, binary.BigEndian, &bits); err != nil {
		return 0, err
	}
	return math.Float64frombits(bits), nil
}

func amfReadAny(r *bytes.Reader) interface{} {
	marker, err := r.ReadByte()
	if err != nil {
		return nil
	}
	switch marker {
	case 0x00: // Number
		var bits uint64
		_ = binary.Read(r, binary.BigEndian, &bits)
		return math.Float64frombits(bits)
	case 0x01: // Boolean
		b, _ := r.ReadByte()
		return b != 0
	case 0x02: // String
		var length uint16
		_ = binary.Read(r, binary.BigEndian, &length)
		strBytes := make([]byte, length)
		_, _ = io.ReadFull(r, strBytes)
		return string(strBytes)
	default:
		return nil
	}
}
