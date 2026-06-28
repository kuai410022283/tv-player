package services

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type TimeChunk struct {
	Data      []byte
	Timestamp time.Time
}

// TimeBasedBuffer caches chunks of data and evicts them based on time
type TimeBasedBuffer struct {
	chunks    []TimeChunk
	retention time.Duration
	mu        sync.RWMutex
}

func NewTimeBasedBuffer(retention time.Duration) *TimeBasedBuffer {
	return &TimeBasedBuffer{
		chunks:    make([]TimeChunk, 0),
		retention: retention,
	}
}

func (tb *TimeBasedBuffer) Write(p []byte) (n int, err error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	chunk := make([]byte, len(p))
	copy(chunk, p)

	tb.chunks = append(tb.chunks, TimeChunk{
		Data:      chunk,
		Timestamp: time.Now(),
	})

	cutoff := time.Now().Add(-tb.retention)
	validIdx := -1
	for i, c := range tb.chunks {
		if c.Timestamp.After(cutoff) {
			validIdx = i
			break
		}
	}
	
	if validIdx == -1 {
		// All chunks are old
		tb.chunks = make([]TimeChunk, 0)
	} else if validIdx > 0 {
		// Create a new slice to allow old chunks to be GC'd
		newLen := len(tb.chunks) - validIdx
		newChunks := make([]TimeChunk, newLen)
		copy(newChunks, tb.chunks[validIdx:])
		tb.chunks = newChunks
	}

	return len(p), nil
}

func (tb *TimeBasedBuffer) Snapshot() []byte {
	tb.mu.RLock()
	defer tb.mu.RUnlock()
	
	var totalLen int
	for _, c := range tb.chunks {
		totalLen += len(c.Data)
	}
	
	if totalLen == 0 {
		return []byte{}
	}
	
	res := make([]byte, totalLen)
	offset := 0
	for _, c := range tb.chunks {
		copy(res[offset:], c.Data)
		offset += len(c.Data)
	}
	
	return res
}

func (tb *TimeBasedBuffer) Clear() {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	tb.chunks = make([]TimeChunk, 0)
}

// ChannelBroadcaster multiplexes a single upstream stream to multiple clients
type ChannelBroadcaster struct {
	ChannelID int64
	URL       string
	Header    http.Header
	buffer    *TimeBasedBuffer
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
		buffer:    NewTimeBasedBuffer(3500 * time.Millisecond), // 3.5s burst buffer
		clients:   make(map[string]chan []byte),
		cancel:    cancel,
		active:    true,
	}
}

func (cb *ChannelBroadcaster) AddClientAndGetSnapshot(sessionID string, ch chan []byte) []byte {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	snapshot := cb.buffer.Snapshot()
	cb.clients[sessionID] = ch
	return snapshot
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
	cb.mu.Lock()
	defer cb.mu.Unlock()

	_, _ = cb.buffer.Write(data)
	
	var slowClients []string

	for sessionID, ch := range cb.clients {
		select {
		case ch <- data:
		default:
			// If client channel is full, they are too slow. Record to disconnect them.
			slowClients = append(slowClients, sessionID)
		}
	}

	if len(slowClients) > 0 {
		for _, sessionID := range slowClients {
			if ch, ok := cb.clients[sessionID]; ok {
				close(ch)
				delete(cb.clients, sessionID)
			}
		}
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
