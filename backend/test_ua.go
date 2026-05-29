package main

import (
	"fmt"
	"net/http"
)

func main() {
	url := "https://mg.28918185.xyz:2026/608807420"
	
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "VLC/3.0.16 LibVLC/3.0.16")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	fmt.Printf("VLC UA Status: %v\n", resp.Status)

	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; TV) AppleWebKit/537.36 TV-Player")
	resp2, err2 := client.Do(req)
	if err2 == nil {
		defer resp2.Body.Close()
		fmt.Printf("Android UA Status: %v\n", resp2.Status)
	}

	req.Header.Set("User-Agent", "Dalvik/2.1.0 (Linux; U; Android 11)")
	resp3, err3 := client.Do(req)
	if err3 == nil {
		defer resp3.Body.Close()
		fmt.Printf("Dalvik UA Status: %v\n", resp3.Status)
	}
}
