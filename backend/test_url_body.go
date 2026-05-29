//go:build ignore

package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
)

func main() {
	url := "https://mg.28918185.xyz:2026/608807420"
	
	resp, err := http.Get(url)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	defer resp.Body.Close()
	
	body, _ := ioutil.ReadAll(resp.Body)
	fmt.Printf("Status: %v\n", resp.Status)
	fmt.Printf("Body: %s\n", string(body))
}
