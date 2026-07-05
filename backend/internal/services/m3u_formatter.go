package services

import (
	"fmt"
	"strings"
)

// FormatToM3U parses the raw content and formats it into a standard M3U format
func FormatToM3U(content string) (string, error) {
	channels, _, err := ParseM3U(strings.NewReader(content))
	if err != nil {
		return "", err
	}
	if len(channels) == 0 {
		return "", fmt.Errorf("未识别到有效的频道数据")
	}

	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")

	for _, ch := range channels {
		sb.WriteString("#EXTINF:-1")
		
		if val, ok := ch["tvg-id"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` tvg-id="%s"`, val))
		}
		if val, ok := ch["tvg-logo"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` tvg-logo="%s"`, val))
		}
		if val, ok := ch["group-title"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` group-title="%s"`, val))
		}
		if val, ok := ch["catchup"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` catchup="%s"`, val))
		}
		if val, ok := ch["catchup-source"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` catchup-source="%s"`, val))
		}
		if val, ok := ch["catchup-days"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` catchup-days="%s"`, val))
		}
		if val, ok := ch["fcc"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` fcc="%s"`, val))
		}
		if val, ok := ch["fcc-type"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf(` fcc-type="%s"`, val))
		}

		name := ch["name"]
		if name == "" {
			name = "未命名频道"
		}
		sb.WriteString(fmt.Sprintf(",%s\n", name))

		if val, ok := ch["user_agent"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf("#EXTVLCOPT:http-user-agent=%s\n", val))
		}
		if val, ok := ch["http-referrer"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf("#EXTVLCOPT:http-referrer=%s\n", val))
		}
		if val, ok := ch["http-origin"]; ok && val != "" {
			sb.WriteString(fmt.Sprintf("#EXTVLCOPT:http-origin=%s\n", val))
		}

		sb.WriteString(fmt.Sprintf("%s\n", ch["url"]))
	}

	return sb.String(), nil
}

// FormatToTXT parses the raw content and formats it into standard TXT format (Group,#genre#)
func FormatToTXT(content string) (string, error) {
	channels, _, err := ParseM3U(strings.NewReader(content))
	if err != nil {
		return "", err
	}
	if len(channels) == 0 {
		return "", fmt.Errorf("未识别到有效的频道数据")
	}

	// Group channels by group-title
	// Use slice to preserve order of groups as they appear
	var groupOrder []string
	groupedChannels := make(map[string][]map[string]string)

	for _, ch := range channels {
		groupName := ch["group-title"]
		if groupName == "" || groupName == "-" {
			groupName = "未分类"
		}
		if _, exists := groupedChannels[groupName]; !exists {
			groupOrder = append(groupOrder, groupName)
		}
		groupedChannels[groupName] = append(groupedChannels[groupName], ch)
	}

	var sb strings.Builder
	for _, groupName := range groupOrder {
		sb.WriteString(fmt.Sprintf("%s,#genre#\n", groupName))
		for _, ch := range groupedChannels[groupName] {
			name := ch["name"]
			if name == "" {
				name = "未命名频道"
			}
			sb.WriteString(fmt.Sprintf("%s,%s\n", name, ch["url"]))
		}
	}

	return sb.String(), nil
}
