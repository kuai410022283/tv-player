package services

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tvplayer/backend/internal/config"
	"github.com/tvplayer/backend/internal/models"
)

// StreamProxy manages proxied streams with health checking
type StreamProxy struct {
	cfg       *config.StreamConfig
	mu        sync.RWMutex
	streams   map[int64]*streamState
	client    *http.Client
	channelSvc *ChannelService
}

type streamState struct {
	ChannelID  int64
	URL        string
	Status     string // playing, buffering, error, stopped
	Bitrate    int64
	BufferPct  int
	ErrorMsg   string
	StartedAt  time.Time
	LastActive time.Time
}

func NewStreamProxy(cfg *config.StreamConfig, channelSvc *ChannelService) *StreamProxy {
	sp := &StreamProxy{
		cfg:        cfg,
		streams:    make(map[int64]*streamState),
		channelSvc: channelSvc,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	os.MkdirAll(cfg.CacheDir, 0755)
	return sp
}

// CheckHealth verifies a stream URL is reachable and returns stream info
func (sp *StreamProxy) CheckHealth(url, streamType string) (*models.StreamStatus, error) {
	status := &models.StreamStatus{
		URL:    url,
		Status: "unknown",
	}

	switch streamType {
	case "hls", "mp4", "dash", "flv":
		resp, err := sp.client.Head(url)
		if err != nil {
			status.Status = "error"
			status.ErrorMsg = err.Error()
			return status, err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			status.Status = "online"
		} else {
			status.Status = "offline"
			status.ErrorMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	case "rtmp", "rtsp":
		// For RTMP/RTSP, we just try a TCP connection
		status.Status = "online" // simplified; real impl would use ffmpeg probe
	default:
		status.Status = "unknown"
	}
	return status, nil
}

// StartHealthCheck runs periodic health checks on all channels
func (sp *StreamProxy) StartHealthCheck(stop <-chan struct{}) {
	interval := time.Duration(sp.cfg.HealthCheckSec) * time.Second
	if interval < 10 { interval = 10 * time.Second }

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			sp.checkAllChannels()
		}
	}
}

func (sp *StreamProxy) checkAllChannels() {
	// simplified: in production, paginate through all channels
	p := &models.PageRequest{Page: 1, PageSize: 200}
	resp, err := sp.channelSvc.ListChannels(0, false, "", p)
	if err != nil { return }

	channels, ok := resp.Items.([]models.Channel)
	if !ok { return }

	for _, ch := range channels {
		status, _ := sp.CheckHealth(ch.StreamURL, ch.StreamType)
		newStatus := "offline"
		if status.Status == "online" {
			newStatus = "online"
		}
		sp.channelSvc.UpdateStatus(ch.ID, newStatus)
	}
}

// GetProxyURL returns the proxied URL for a channel
func (sp *StreamProxy) GetProxyURL(channelID int64, baseURL string) string {
	return fmt.Sprintf("%s/api/v1/stream/proxy/%d", baseURL, channelID)
}

// ServeStream proxies the actual stream data
func (sp *StreamProxy) ServeStream(channelID int64, w http.ResponseWriter, r *http.Request) error {
	ch, err := sp.channelSvc.GetChannel(channelID)
	if err != nil {
		return fmt.Errorf("channel not found: %w", err)
	}

	// Update stream state
	sp.mu.Lock()
	sp.streams[channelID] = &streamState{
		ChannelID:  channelID,
		URL:        ch.StreamURL,
		Status:     "playing",
		StartedAt:  time.Now(),
		LastActive: time.Now(),
	}
	sp.mu.Unlock()

	defer func() {
		sp.mu.Lock()
		delete(sp.streams, channelID)
		sp.mu.Unlock()
	}()

	// Proxy the stream
	req, err := http.NewRequestWithContext(r.Context(), "GET", ch.StreamURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TVPlayer/1.0")

	resp, err := sp.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Copy headers
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream with buffering
	buf := make([]byte, sp.cfg.BufferSize)
	reader := bufio.NewReaderSize(resp.Body, sp.cfg.BufferSize)
	
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			sp.mu.Lock()
			if s, ok := sp.streams[channelID]; ok {
				s.LastActive = time.Now()
			}
			sp.mu.Unlock()
		}
		if err != nil {
			if err == io.EOF { return nil }
			return err
		}
	}
}

// GetActiveStreams returns currently active stream states
func (sp *StreamProxy) GetActiveStreams() []streamState {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	var streams []streamState
	for _, s := range sp.streams {
		streams = append(streams, *s)
	}
	return streams
}

// M3U parsing
func ParseM3U(reader io.Reader) ([]map[string]string, error) {
	scanner := bufio.NewScanner(reader)
	var channels []map[string]string
	var current map[string]string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" { continue }

		if strings.HasPrefix(line, "#EXTINF:") {
			current = parseExtInf(line)
		} else if !strings.HasPrefix(line, "#") && current != nil {
			current["url"] = line
			channels = append(channels, current)
			current = nil
		}
	}
	return channels, scanner.Err()
}

func parseExtInf(line string) map[string]string {
	ch := make(map[string]string)
	ch["raw"] = line

	// Extract name (after last comma)
	if idx := strings.LastIndex(line, ","); idx >= 0 {
		ch["name"] = strings.TrimSpace(line[idx+1:])
	}

	// Extract attributes
	attrs := []string{"tvg-id", "tvg-name", "tvg-logo", "group-title", "tvg-chno"}
	for _, attr := range attrs {
		prefix := attr + "=\""
		if start := strings.Index(line, prefix); start >= 0 {
			start += len(prefix)
			if end := strings.Index(line[start:], "\""); end >= 0 {
				ch[attr] = line[start : start+end]
			}
		}
	}
	return ch
}

// ParseM3UFile parses an M3U file from disk
func ParseM3UFile(path string) ([]map[string]string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil { return nil, err }
	defer f.Close()
	return ParseM3U(f)
}
