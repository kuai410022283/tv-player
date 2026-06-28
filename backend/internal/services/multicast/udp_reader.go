package multicast

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// bufferPool for 64KB UDP reads
var udpBufferPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 65536)
		return &buf
	},
}

const maxJitterPackets = 128 // max buffered packets (approx 100-200ms depending on bitrate)

type MulticastReader struct {
	conn       *net.UDPConn
	buffer     *[]byte
	isRTP      bool

	remainderBuf [65536]byte
	remainderOff int
	remainderLen int

	// Jitter Buffer fields (zero-allocation)
	jitterBuf   [maxJitterPackets][2048]byte
	jitterLen   [maxJitterPackets]int
	jitterSeq   [maxJitterPackets]uint16
	jitterValid [maxJitterPackets]bool
	
	nextSeq   uint16
	hasSeq    bool
	mu        sync.Mutex
}

func NewMulticastReader(ctx context.Context, rawURL string) (*MulticastReader, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid multicast url: %w", err)
	}

	host := u.Hostname()
	portStr := u.Port()
	if portStr == "" {
		portStr = "1234"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}

	mcastIP := net.ParseIP(host)
	if mcastIP == nil {
		return nil, fmt.Errorf("invalid multicast ip: %s", host)
	}

	addr := &net.UDPAddr{
		IP:   mcastIP,
		Port: port,
	}

	var conn *net.UDPConn
	if mcastIP.IsMulticast() {
		conn, err = net.ListenMulticastUDP("udp", nil, addr)
		if err != nil {
			return nil, fmt.Errorf("failed to join multicast group: %w", err)
		}
	} else {
		conn, err = net.ListenUDP("udp", addr)
		if err != nil {
			return nil, fmt.Errorf("failed to listen on udp: %w", err)
		}
	}

	_ = conn.SetReadBuffer(4 * 1024 * 1024)

	bufPtr := udpBufferPool.Get().(*[]byte)

	return &MulticastReader{
		conn:   conn,
		buffer: bufPtr,
		isRTP:  u.Scheme == "rtp",
	}, nil
}

// seqDiff computes the forward difference taking uint16 wraparound into account
func seqDiff(a, b uint16) int16 {
	return int16(a - b)
}

func (r *MulticastReader) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// 1. Flush any remainder from the last read
	if r.remainderLen > 0 {
		n = copy(p, r.remainderBuf[r.remainderOff:r.remainderOff+r.remainderLen])
		r.remainderOff += n
		r.remainderLen -= n
		return n, nil
	}

	// 2. Check if the next consecutive packet is already in the jitter buffer
	if r.hasSeq {
		nextIdx := r.nextSeq % maxJitterPackets
		if r.jitterValid[nextIdx] && r.jitterSeq[nextIdx] == r.nextSeq {
			r.jitterValid[nextIdx] = false
			r.nextSeq++
			return r.emitPayload(p, r.jitterBuf[nextIdx][:r.jitterLen[nextIdx]])
		}
	}

	// 3. Read from UDP loop
	for {
		_ = r.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		nRead, _, err := r.conn.ReadFromUDP(*r.buffer)
		if err != nil {
			return 0, err
		}

		data := (*r.buffer)[:nRead]

		// 智能动态检测 RTP：TS 流同步字节为 0x47 (01000111)。RTP v2 头部首字节为 0x80 (10xxxxxx)。
		// 掩码 &0xC0 下可以完美区分。哪怕源地址写的是 udp://，只要检测到 RTP 头，就自动开启脱壳逻辑。
		isRTPPacket := len(data) >= 12 && (data[0]&0xC0) == 0x80

		if !isRTPPacket && !r.isRTP {
			// 不是 RTP 格式，且不是 rtp:// 协议，按裸流透传
			return r.emitPayload(p, data)
		}

		if isRTPPacket {
			seq := uint16(data[2])<<8 | uint16(data[3])
			payloadStart := 12
			payloadStart += int(data[0]&0x0F) * 4 // CSRC headers
			if (data[0] & 0x10) != 0 {            // Extension header
				if len(data) >= payloadStart+4 {
					extLen := int(data[payloadStart+2])<<8 | int(data[payloadStart+3])
					payloadStart += 4 + 4*extLen
				}
			}

			payloadLength := nRead - payloadStart
			if (data[0] & 0x20) != 0 { // Padding
				payloadLength -= int(data[nRead-1])
			}

			if payloadLength <= 0 || payloadStart+payloadLength > nRead {
				payloadStart = 0
				payloadLength = nRead
			}

			if !r.hasSeq {
				r.nextSeq = seq
				r.hasSeq = true
			}

			diff := seqDiff(seq, r.nextSeq)

			if diff < 0 && diff > -1000 {
				continue // Late packet, drop
			} else if diff > 1000 {
				// Massive jump, reset
				r.nextSeq = seq
				for i := 0; i < maxJitterPackets; i++ {
					r.jitterValid[i] = false
				}
			}

			if seq == r.nextSeq {
				r.nextSeq++
				return r.emitPayload(p, data[payloadStart:payloadStart+payloadLength])
			} else {
				idx := seq % maxJitterPackets
				if payloadLength <= 2048 {
					copy(r.jitterBuf[idx][:], data[payloadStart:payloadStart+payloadLength])
					r.jitterLen[idx] = payloadLength
					r.jitterSeq[idx] = seq
					r.jitterValid[idx] = true
				}

				// Check if the packet we just received happened to fill the gap
				nextIdx := r.nextSeq % maxJitterPackets
				if r.jitterValid[nextIdx] && r.jitterSeq[nextIdx] == r.nextSeq {
					r.jitterValid[nextIdx] = false
					r.nextSeq++
					return r.emitPayload(p, r.jitterBuf[nextIdx][:r.jitterLen[nextIdx]])
				}

				// Buffer full check
				validCount := 0
				for i := 0; i < maxJitterPackets; i++ {
					if r.jitterValid[i] {
						validCount++
					}
				}
				if validCount >= maxJitterPackets-1 {
					var lowestSeq uint16
					var lowestDiff int16 = 32767
					for i := 0; i < maxJitterPackets; i++ {
						if r.jitterValid[i] {
							d := seqDiff(r.jitterSeq[i], r.nextSeq)
							if d >= 0 && d < lowestDiff {
								lowestDiff = d
								lowestSeq = r.jitterSeq[i]
							}
						}
					}

					lIdx := lowestSeq % maxJitterPackets
					r.jitterValid[lIdx] = false
					r.nextSeq = lowestSeq + 1
					return r.emitPayload(p, r.jitterBuf[lIdx][:r.jitterLen[lIdx]])
				}
			}
		} else {
			// Not a valid RTP packet, but requested RTP. Fallback to raw.
			return r.emitPayload(p, data)
		}
	}
}

func (r *MulticastReader) emitPayload(p []byte, payload []byte) (int, error) {
	n := copy(p, payload)
	if n < len(payload) {
		r.remainderLen = len(payload) - n
		r.remainderOff = 0
		copy(r.remainderBuf[:], payload[n:])
	}
	return n, nil
}

func (r *MulticastReader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if r.buffer != nil {
		udpBufferPool.Put(r.buffer)
		r.buffer = nil
	}
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}
