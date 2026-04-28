package services

import (
	"testing"
)

func TestValidateStreamURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		// 合法 URL
		{"正常 HLS 流", "http://example.com/live/stream.m3u8", false, ""},
		{"正常 HTTPS 流", "https://cdn.example.com/video.mp4", false, ""},
		{"正常 RTMP 流", "rtmp://stream.example.com/live/key", false, ""},
		{"正常 RTSP 流", "rtsp://camera.example.com/stream1", false, ""},

		// 空 URL
		{"空 URL", "", true, "URL 不能为空"},

		// 协议限制
		{"file 协议", "file:///etc/passwd", true, "禁止的协议"},
		{"gopher 协议", "gopher://127.0.0.1:70/", true, "禁止的协议"},
		{"data 协议", "data:text/plain;base64,SGVsbG8=", true, "禁止的协议"},

		// 内网地址
		{"localhost", "http://localhost:8080/stream", true, "禁止访问"},
		{"127.0.0.1", "http://127.0.0.1:9527/stream", true, "禁止访问"},
		{"10.x.x.x", "http://10.0.0.1/stream", true, "禁止访问"},
		{"192.168.x.x", "http://192.168.1.100/stream", true, "禁止访问"},
		{"172.16.x.x", "http://172.16.0.1/stream", true, "禁止访问"},
		{"云元数据服务", "http://169.254.169.254/latest/meta-data/", true, "禁止访问"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStreamURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStreamURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateStreamURL(%q) error = %q, want containing %q", tt.url, err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
