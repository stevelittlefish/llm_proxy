package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/url"
	"strings"

	"llm_proxy/database"
)

type logListEntry struct {
	database.LogEntry
	Preview      string
	PreviewParts []renderedLogPart
}

type renderedLogMessage struct {
	Role    string
	Parts   []renderedLogPart
	Summary string
}

type renderedLogPart struct {
	Kind      string
	Text      string
	URL       template.URL
	MIME      string
	Size      int
	SizeLabel string
	Width     int
	Height    int
	Summary   string
}

func makeLogListEntry(entry database.LogEntry) logListEntry {
	preview := lastMessageSummaryFromRaw(entry.FrontendRequest)
	parts := []renderedLogPart(nil)
	messages := renderedMessagesFromRaw(entry.FrontendRequest)
	if len(messages) > 0 {
		parts = messages[len(messages)-1].Parts
	}
	if preview == "" {
		preview = entry.LastMessage
	}
	return logListEntry{LogEntry: entry, Preview: preview, PreviewParts: parts}
}

func promptDisplayForEntry(entry *database.LogEntry) string {
	if summary := marshalLogMessagesSummary(entry.FrontendRequest); summary != "" {
		return summary
	}
	return entry.Prompt
}

func renderedMessagesFromRaw(rawJSON string) []renderedLogMessage {
	msgs := parseMessages(rawJSON)
	out := make([]renderedLogMessage, 0, len(msgs))
	for _, msg := range msgs {
		parts := renderedPartsFromContent(msg.Content)
		out = append(out, renderedLogMessage{
			Role:    msg.Role,
			Parts:   parts,
			Summary: summarizeRenderedParts(parts),
		})
	}
	return out
}

func renderedPartsFromContent(content interface{}) []renderedLogPart {
	switch v := content.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []renderedLogPart{{Kind: "text", Text: v, Summary: v}}
	case []interface{}:
		parts := make([]renderedLogPart, 0, len(v))
		for _, item := range v {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			partType, _ := part["type"].(string)
			switch partType {
			case "", "text":
				if text, ok := part["text"].(string); ok && text != "" {
					parts = append(parts, renderedLogPart{Kind: "text", Text: text, Summary: text})
				}
			case "image_url":
				if imagePart := renderedImagePart(part); imagePart.Summary != "" {
					parts = append(parts, imagePart)
				}
			default:
				parts = append(parts, renderedLogPart{Kind: "other", Summary: fmt.Sprintf("[content: %s]", partType)})
			}
		}
		return parts
	}
	return nil
}

func renderedImagePart(part map[string]interface{}) renderedLogPart {
	imageURL, _ := part["image_url"].(map[string]interface{})
	if imageURL == nil {
		return renderedLogPart{}
	}
	rawURL, _ := imageURL["url"].(string)
	if rawURL == "" {
		return renderedLogPart{}
	}

	p := renderedLogPart{Kind: "image", URL: safeImageURL(rawURL)}
	if info, ok := parseDataImageURL(rawURL); ok {
		p.MIME = info.mime
		p.Size = info.size
		p.SizeLabel = formatBytes(info.size)
		p.Width = info.width
		p.Height = info.height
		p.Summary = imageSummary(p)
		return p
	}

	p.MIME = "remote"
	p.Summary = fmt.Sprintf("[image: %s]", summarizeURL(rawURL, 80))
	return p
}

type dataImageInfo struct {
	mime          string
	size          int
	width, height int
}

func parseDataImageURL(rawURL string) (dataImageInfo, bool) {
	if !strings.HasPrefix(rawURL, "data:image/") {
		return dataImageInfo{}, false
	}
	comma := strings.IndexByte(rawURL, ',')
	if comma == -1 {
		return dataImageInfo{}, false
	}
	meta := rawURL[len("data:"):comma]
	payload := rawURL[comma+1:]
	mime := meta
	if semi := strings.IndexByte(mime, ';'); semi != -1 {
		mime = mime[:semi]
	}
	if !strings.HasPrefix(mime, "image/") {
		return dataImageInfo{}, false
	}

	var data []byte
	var err error
	if strings.Contains(meta, ";base64") {
		compact := strings.Map(func(r rune) rune {
			switch r {
			case '\n', '\r', '\t', ' ':
				return -1
			default:
				return r
			}
		}, payload)
		data, err = base64.StdEncoding.DecodeString(compact)
	} else {
		var decoded string
		decoded, err = url.QueryUnescape(payload)
		data = []byte(decoded)
	}
	if err != nil {
		return dataImageInfo{mime: mime}, true
	}

	info := dataImageInfo{mime: mime, size: len(data)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		info.width = cfg.Width
		info.height = cfg.Height
	}
	return info, true
}

func imageSummary(p renderedLogPart) string {
	bits := []string(nil)
	if p.MIME != "" {
		bits = append(bits, p.MIME)
	}
	if p.Width > 0 && p.Height > 0 {
		bits = append(bits, fmt.Sprintf("%d×%d", p.Width, p.Height))
	}
	if p.SizeLabel != "" {
		bits = append(bits, p.SizeLabel)
	}
	if len(bits) == 0 {
		return "[image]"
	}
	return "[image: " + strings.Join(bits, ", ") + "]"
}

func summarizeRenderedParts(parts []renderedLogPart) string {
	if len(parts) == 0 {
		return ""
	}
	summaries := make([]string, 0, len(parts))
	for _, part := range parts {
		if part.Kind == "text" {
			if part.Text != "" {
				summaries = append(summaries, part.Text)
			}
			continue
		}
		if part.Summary != "" {
			summaries = append(summaries, part.Summary)
		}
	}
	return strings.Join(summaries, " ")
}

func contentSummary(c interface{}) string {
	return summarizeRenderedParts(renderedPartsFromContent(c))
}

func lastMessageSummaryFromRaw(rawJSON string) string {
	msgs := renderedMessagesFromRaw(rawJSON)
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].Summary
}

func promptSummaryFromMessages(messages []logMessage) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Role)
		b.WriteString(": ")
		b.WriteString(contentSummary(msg.Content))
		b.WriteString("\n")
	}
	return b.String()
}

func safeImageURL(rawURL string) template.URL {
	if strings.HasPrefix(rawURL, "data:image/") {
		return template.URL(rawURL)
	}
	if strings.HasPrefix(rawURL, "https://") || strings.HasPrefix(rawURL, "http://") {
		return template.URL(rawURL)
	}
	return ""
}

func summarizeURL(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func marshalLogMessagesSummary(rawJSON string) string {
	var req struct {
		Messages []logMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		return ""
	}
	return promptSummaryFromMessages(req.Messages)
}
