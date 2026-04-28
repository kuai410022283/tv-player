package services

import "testing"

func TestValidateStreamURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"正常 HLS", "http://example.com/live.m3u8", false},
		{"正常 HTTPS", "https://cdn.example.com/video.mp4", false},
		{"空 URL", "", true},
		{"file 协议", "file:///etc/passwd", true},
		{"localhost", "http://localhost:8080/stream", true},
		{"127.0.0.1", "http://127.0.0.1:9527/stream", true},
		{"10.x.x.x", "http://10.0.0.1/stream", true},
		{"192.168.x.x", "http://192.168.1.100/stream", true},
		{"云元数据", "http://169.254.169.254/latest/meta-data/", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStreamURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStreamURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
