package main

import (
	"fmt"
	"net/http"
	"crypto/tls"
)

func main() {
	url := "https://mg.28918185.xyz:2026/608807420"
	
	// Try without skipping TLS verification
	resp, err := http.Head(url)
	if err != nil {
		fmt.Printf("Strict TLS Error: %v\n", err)
	} else {
		fmt.Printf("Strict TLS Success: %v\n", resp.Status)
		resp.Body.Close()
	}

	// Try with skipping TLS verification
	tr := &http.Transport{
        TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
    }
    client := &http.Client{Transport: tr}
    resp, err = client.Head(url)
	if err != nil {
		fmt.Printf("Insecure TLS Error: %v\n", err)
	} else {
		fmt.Printf("Insecure TLS Success: %v\n", resp.Status)
		for k, v := range resp.Header {
			fmt.Printf("%s: %s\n", k, v)
		}
		resp.Body.Close()
	}
}
