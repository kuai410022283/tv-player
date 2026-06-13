package services

import (
	"context"
	"net/http"
	"sync"
)

// RingBuffer is a simple thread-safe byte buffer for caching stream bursts
type RingBuffer struct {
	buf []byte
	max int
	mu  sync.RWMutex
}

func NewRingBuffer(max int) *RingBuffer {
	return &RingBuffer{
		buf: make([]byte, 0, max),
		max: max,
	}
}

func (rb *RingBuffer) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.buf = append(rb.buf, p...)
	if len(rb.buf) > rb.max {
		cut := len(rb.buf) - rb.max
		// Shift elements to the left to reuse capacity
		copy(rb.buf, rb.buf[cut:])
		rb.buf = rb.buf[:rb.max]
	}
	return len(p), nil
}

func (rb *RingBuffer) Snapshot() []byte {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	
	// FCC Optimization: Find the last I-Frame to start the burst cleanly
	// Search backwards for NALU start code 0x00 00 00 01
	startIndex := 0
	for i := len(rb.buf) - 5; i >= 0; i-- {
		if rb.buf[i] == 0 && rb.buf[i+1] == 0 && rb.buf[i+2] == 0 && rb.buf[i+3] == 1 {
			nalType := rb.buf[i+4] & 0x1F
			// H.264: 7 is SPS (usually start of GOP), 5 is IDR
			if nalType == 7 || nalType == 5 {
				startIndex = i
				break
			}
			// H.265 (HEVC): NAL unit type is in bits 1-6
			hevcType := (rb.buf[i+4] & 0x7E) >> 1
			if hevcType == 32 || hevcType == 33 || hevcType == 19 || hevcType == 20 || hevcType == 21 {
				startIndex = i
				break
			}
		}
	}

	snapshotLen := len(rb.buf) - startIndex
	if snapshotLen <= 0 {
		return []byte{}
	}
	
	res := make([]byte, snapshotLen)
	copy(res, rb.buf[startIndex:])
	return res
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf = rb.buf[:0]
}

// ChannelBroadcaster multiplexes a single upstream stream to multiple clients
type ChannelBroadcaster struct {
	ChannelID int64
	URL       string
	Header    http.Header
	buffer    *RingBuffer
	clients   map[string]chan []byte
	mu        sync.RWMutex
	cancel    context.CancelFunc
	active    bool
}

func NewChannelBroadcaster(channelID int64, targetURL string, header http.Header, cancel context.CancelFunc) *ChannelBroadcaster {
	return &ChannelBroadcaster{
		ChannelID: channelID,
		URL:       targetURL,
		Header:    header,
		buffer:    NewRingBuffer(8 * 1024 * 1024), // 8MB burst buffer，解决起播慢问题
		clients:   make(map[string]chan []byte),
		cancel:    cancel,
		active:    true,
	}
}

func (cb *ChannelBroadcaster) AddClient(sessionID string, ch chan []byte) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.clients[sessionID] = ch
}

func (cb *ChannelBroadcaster) RemoveClient(sessionID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	delete(cb.clients, sessionID)
}

func (cb *ChannelBroadcaster) ClientCount() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return len(cb.clients)
}

func (cb *ChannelBroadcaster) Broadcast(data []byte) {
	_, _ = cb.buffer.Write(data)
	
	var slowClients []string

	cb.mu.RLock()
	for sessionID, ch := range cb.clients {
		select {
		case ch <- data:
		default:
			// If client channel is full, they are too slow. Record to disconnect them.
			slowClients = append(slowClients, sessionID)
		}
	}
	cb.mu.RUnlock()

	if len(slowClients) > 0 {
		cb.mu.Lock()
		for _, sessionID := range slowClients {
			if ch, ok := cb.clients[sessionID]; ok {
				close(ch)
				delete(cb.clients, sessionID)
			}
		}
		cb.mu.Unlock()
	}
}

func (cb *ChannelBroadcaster) Stop() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if !cb.active {
		return
	}
	cb.active = false
	if cb.cancel != nil {
		cb.cancel()
	}
	for _, ch := range cb.clients {
		close(ch)
	}
	cb.clients = make(map[string]chan []byte)
	cb.buffer.Clear()
}
