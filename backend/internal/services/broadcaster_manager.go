package services

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mediaplayer/backend/internal/models"
)

// serveMultiplex handles the client request using the ChannelBroadcaster
func (sp *StreamProxy) serveMultiplex(channelID int64, clientID int64, clientIP string, clientName string, w http.ResponseWriter, r *http.Request, ch *models.Channel) error {
	cb, err := sp.getOrCreateBroadcaster(channelID, ch)
	if err != nil {
		return err
	}

	// Copy HTTP headers from the original response to the client
	for k, v := range cb.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(http.StatusOK)

	// Create client channel
	sessionID := fmt.Sprintf("mux-%d-%d-%d", channelID, clientID, time.Now().UnixNano())
	clientChan := make(chan []byte, 2048)
	
	// Create cancel context for killing stream
	ctx, cancel := context.WithCancel(r.Context())

	// Add client
	cb.AddClient(sessionID, clientChan)

	// Update stream state for tracking
	sp.mu.Lock()
	sp.streams[sessionID] = &models.ActiveStream{
		Mu:          &sync.RWMutex{},
		SessionID:   sessionID,
		ChannelID:   channelID,
		ChannelName: ch.Name,
		ClientID:    clientID,
		ClientName:  clientName,
		ClientIP:    clientIP,
		URL:         cb.URL,
		Status:      "playing(mux)",
		StartedAt:   time.Now(),
		LastActive:  time.Now(),
	}
	sp.cancels[sessionID] = cancel
	sp.mu.Unlock()

	defer func() {
		cb.RemoveClient(sessionID)
		cancel()
		sp.mu.Lock()
		delete(sp.streams, sessionID)
		delete(sp.cancels, sessionID)
		sp.mu.Unlock()
		
		// Auto cleanup: if no clients left, stop the broadcaster to save upstream bandwidth
		if cb.ClientCount() == 0 {
			sp.bMu.Lock()
			if existing, ok := sp.broadcasters[channelID]; ok && existing == cb && existing.ClientCount() == 0 {
				existing.Stop()
				delete(sp.broadcasters, channelID)
			}
			sp.bMu.Unlock()
		}
	}()

	var bytesSinceLastUpdate int64 = 0
	lastUpdate := time.Now()

	// Send burst cache (FCC)
	snapshot := cb.buffer.Snapshot()
	if len(snapshot) > 0 {
		n, err := w.Write(snapshot)
		if err != nil {
			return err
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		bytesSinceLastUpdate += int64(n)
	}

	// Stream loop
	writeBuf := make([]byte, 0, 128*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		case chunk, ok := <-clientChan:
			if !ok {
				if len(writeBuf) > 0 {
					w.Write(writeBuf)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				return nil
			}
			writeBuf = append(writeBuf, chunk...)
			if len(writeBuf) >= 128*1024 {
				n, err := w.Write(writeBuf)
				if err != nil {
					return err
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				bytesSinceLastUpdate += int64(n)
				writeBuf = writeBuf[:0]
			}

			now := time.Now()
			if now.Sub(lastUpdate) >= time.Second {
				if len(writeBuf) > 0 {
					n, err := w.Write(writeBuf)
					if err != nil {
						return err
					}
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					bytesSinceLastUpdate += int64(n)
					writeBuf = writeBuf[:0]
				}

				sp.mu.RLock()
				if s, ok := sp.streams[sessionID]; ok {
					s.Mu.Lock()
					s.LastActive = now
					s.SpeedBytes = bytesSinceLastUpdate
					s.Mu.Unlock()
				}
				sp.mu.RUnlock()
				bytesSinceLastUpdate = 0
				lastUpdate = now
			}
		}
	}
}

func (sp *StreamProxy) getOrCreateBroadcaster(channelID int64, ch *models.Channel) (*ChannelBroadcaster, error) {
	sp.bMu.Lock()
	cb, exists := sp.broadcasters[channelID]
	if exists && cb.active {
		sp.bMu.Unlock()
		return cb, nil
	}
	sp.bMu.Unlock()

	// Connect Upstream
	streamToProxy := ch.StreamURL
	rawURLs := strings.Split(streamToProxy, "#")
	validURLs := []string{}
	for _, u := range rawURLs {
		if strings.TrimSpace(u) != "" {
			validURLs = append(validURLs, strings.TrimSpace(u))
		}
	}

	if len(validURLs) == 0 {
		return nil, fmt.Errorf("无有效播放线路")
	}

	ua, headers, _ := sp.channelSvc.GetInheritedHeaders(channelID)
	if ua == "" {
		ua = "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player"
	}

	ctx, cancel := context.WithCancel(context.Background())

	type raceResult struct {
		index int
		resp  *http.Response
		url   string
		err   error
	}

	resultChan := make(chan raceResult, len(validURLs))
	reqCancels := make([]context.CancelFunc, len(validURLs))

	for i, u := range validURLs {
		rCtx, rCancel := context.WithCancel(ctx)
		reqCancels[i] = rCancel
		
		go func(idx int, targetURL string, reqCtx context.Context) {
			req, err := http.NewRequestWithContext(reqCtx, "GET", targetURL, nil)
			if err != nil {
				resultChan <- raceResult{index: idx, err: err}
				return
			}
			req.Header.Set("User-Agent", ua)
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			
			rResp, rErr := sp.client.Do(req)
			if rErr != nil {
				resultChan <- raceResult{index: idx, err: rErr}
				return
			}
			if rResp.StatusCode >= 200 && rResp.StatusCode < 400 {
				resultChan <- raceResult{index: idx, resp: rResp, url: targetURL}
			} else {
				rResp.Body.Close()
				resultChan <- raceResult{index: idx, err: fmt.Errorf("status code %d", rResp.StatusCode)}
			}
		}(i, u, rCtx)
	}

	var resp *http.Response
	var finalURL string
	var errorsList []string
	var finalCancel context.CancelFunc
	
	for i := 0; i < len(validURLs); i++ {
		res := <-resultChan
		if res.resp != nil {
			if resp == nil {
				resp = res.resp
				finalURL = res.url
				if res.resp.Request != nil && res.resp.Request.URL != nil {
					finalURL = res.resp.Request.URL.String()
				}
				
				// Self-healing of StreamType
				if ch.StreamType == "" {
					contentType := res.resp.Header.Get("Content-Type")
					isM3U8 := strings.Contains(strings.ToLower(contentType), "mpegurl") ||
						strings.Contains(strings.ToLower(finalURL), ".m3u8")
					var actualType string
					if isM3U8 {
						actualType = "hls"
					} else if strings.Contains(strings.ToLower(contentType), "video/mp2t") || strings.Contains(strings.ToLower(contentType), "octet-stream") {
						actualType = "ts"
					} else if strings.Contains(strings.ToLower(contentType), "flv") {
						actualType = "flv"
					}
					if actualType != "" {
						ch.StreamType = actualType
						_ = sp.channelSvc.UpdateStreamType(ch.ID, actualType)
					}
				}

				// Save cancel for the winning request to attach to broadcaster
				for j, c := range reqCancels {
					if j == res.index {
						finalCancel = c
					} else if c != nil {
						c() // Cancel other requests immediately
					}
				}
			} else {
				res.resp.Body.Close() // We already have a winner, close this slow response
			}
		} else if res.err != nil {
			errorsList = append(errorsList, res.err.Error())
		}
	}
	
	if resp == nil {
		cancel()
		for _, c := range reqCancels {
			if c != nil {
				c()
			}
		}
		return nil, fmt.Errorf("所有复用线路均失效: %s", strings.Join(errorsList, " | "))
	}

	// We got the response and headers!
	sp.bMu.Lock()
	defer sp.bMu.Unlock()
	
	// Double check if another goroutine already created it while we were connecting
	if existing, ok := sp.broadcasters[channelID]; ok && existing.active {
		resp.Body.Close()
		if finalCancel != nil {
			finalCancel()
		}
		cancel()
		return existing, nil
	}

	newCb := NewChannelBroadcaster(channelID, finalURL, resp.Header, func() {
		if finalCancel != nil {
			finalCancel()
		}
		cancel()
	})
	sp.broadcasters[channelID] = newCb

	// Start reader goroutine
	go func(b *ChannelBroadcaster, body io.ReadCloser) {
		defer body.Close()
		defer b.Stop()
		
		bufferSize := 64 * 1024
		buf := make([]byte, bufferSize)
		reader := bufio.NewReaderSize(body, bufferSize)
		
		for {
			if !b.active {
				break
			}
			n, err := reader.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				b.Broadcast(chunk)
			}
			if err != nil {
				break
			}
		}
	}(newCb, resp.Body)

	return newCb, nil
}
