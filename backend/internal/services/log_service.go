package services

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type LogService struct{}

func NewLogService() *LogService {
	os.MkdirAll("library/logs", 0755)
	return &LogService{}
}

func (s *LogService) ReadLogIncremental(path string, cursor int64, maxRead int64) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", 0, nil
		}
		return "", cursor, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", cursor, err
	}
	fileSize := stat.Size()

	if cursor == 0 {
		// First read: fetch last maxRead bytes (e.g. 50KB)
		start := fileSize - maxRead
		if start < 0 {
			start = 0
		}
		file.Seek(start, io.SeekStart)
	} else if cursor > fileSize {
		// Log rotated or truncated
		file.Seek(0, io.SeekStart)
	} else {
		file.Seek(cursor, io.SeekStart)
	}

	data, err := io.ReadAll(file)
	if err != nil && err != io.EOF {
		return "", cursor, err
	}

	newCursor, _ := file.Seek(0, io.SeekCurrent)
	return string(data), newCursor, nil
}

func (s *LogService) ReadBackendLog(cursor int64) (string, int64, error) {
	return s.ReadLogIncremental("./data/logs/backend.log", cursor, 50*1024)
}

type ClientLogInfo struct {
	ClientID string    `json:"client_id"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"mod_time"`
}

func (s *LogService) ListClientLogs() ([]ClientLogInfo, error) {
	entries, err := os.ReadDir("library/logs")
	if err != nil {
		return nil, err
	}
	var logs []ClientLogInfo
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".log" {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			clientID := entry.Name()[:len(entry.Name())-4] // remove .log
			logs = append(logs, ClientLogInfo{
				ClientID: clientID,
				Size:     info.Size(),
				ModTime:  info.ModTime(),
			})
		}
	}
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].ModTime.After(logs[j].ModTime)
	})
	return logs, nil
}

func (s *LogService) ReadClientLog(clientID string, cursor int64) (string, int64, error) {
	path := filepath.Join("library/logs", fmt.Sprintf("%s.log", clientID))
	return s.ReadLogIncremental(path, cursor, 50*1024)
}

func (s *LogService) DeleteClientLog(clientID string) error {
	path := filepath.Join("library/logs", fmt.Sprintf("%s.log", clientID))
	os.Remove(path)
	os.Remove(path + ".bak")
	return nil
}
