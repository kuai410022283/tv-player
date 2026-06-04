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
	res := make([]byte, len(rb.buf))
	copy(res, rb.buf)
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
		buffer:    NewRingBuffer(2 * 1024 * 1024), // 2MB burst buffer
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
	cb.buffer.Write(data)
	
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
