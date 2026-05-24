package handlers

import (
	"encoding/json"
	"fmt"
	"strings"

	"llm_proxy/database"
)

// formatLLMLog produces a markdown document from a log entry suitable for
// pasting into an LLM conversation for debugging.
func formatLLMLog(e *database.LogEntry) string {
	var b strings.Builder

	// ── Header ────────────────────────────────────────────────────────────────
	fmt.Fprintf(&b, "# LLM Proxy Request #%d\n\n", e.ID)

	fmt.Fprintf(&b, "## Overview\n\n")
	fmt.Fprintf(&b, "| Field | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Timestamp | %s |\n", e.Timestamp.UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "| Model | %s |\n", e.Model)
	fmt.Fprintf(&b, "| Endpoint | %s |\n", e.Endpoint)
	statusLabel := fmt.Sprintf("%d", e.StatusCode)
	if e.StatusCode != 200 {
		statusLabel += " ⚠ ERROR"
	}
	fmt.Fprintf(&b, "| Status | %s |\n", statusLabel)
	fmt.Fprintf(&b, "| Latency | %d ms |\n", e.LatencyMs)
	fmt.Fprintf(&b, "| Backend type | %s |\n", e.BackendType)
	fmt.Fprintf(&b, "| Streaming | %v |\n", e.Stream)
	fmt.Fprintf(&b, "| Frontend URL | %s |\n", e.FrontendURL)
	fmt.Fprintf(&b, "| Backend URL | %s |\n\n", e.BackendURL)

	// ── Error ─────────────────────────────────────────────────────────────────
	if e.Error != "" {
		fmt.Fprintf(&b, "## Error\n\n```\n%s\n```\n\n", e.Error)
	}

	// ── Conversations ─────────────────────────────────────────────────────────
	feMessages := parseMessages(e.FrontendRequest)
	beMessages := parseMessages(e.BackendRequest)

	b.WriteString("## Conversation (Frontend Request)\n\n")
	b.WriteString("_What the client sent to the proxy._\n\n")
	writeMessages(&b, feMessages)

	if len(beMessages) > 0 {
		b.WriteString("## Conversation (Backend Request)\n\n")
		b.WriteString("_What the proxy forwarded to the backend, after transformation._\n\n")
		writeMessages(&b, beMessages)
	}

	// ── Final response / backend response ────────────────────────────────────
	if e.Response != "" {
		fmt.Fprintf(&b, "## Model Response\n\n%s\n\n", e.Response)
	}

	if e.BackendResponse != "" {
		b.WriteString("## Raw Backend Response\n\n")
		if pretty := prettyJSON(e.BackendResponse); pretty != "" {
			fmt.Fprintf(&b, "```json\n%s\n```\n\n", pretty)
		} else {
			fmt.Fprintf(&b, "```\n%s\n```\n\n", e.BackendResponse)
		}
	}

	if e.FrontendResponse != "" {
		b.WriteString("## Raw Frontend Response\n\n")
		b.WriteString("_What the proxy sent back to the client._\n\n")
		if pretty := prettyJSON(e.FrontendResponse); pretty != "" {
			fmt.Fprintf(&b, "```json\n%s\n```\n\n", pretty)
		} else {
			// Streaming responses are newline-delimited JSON — pretty-print each line
			fmt.Fprintf(&b, "```\n%s\n```\n\n", prettyNDJSON(e.FrontendResponse))
		}
	}

	return b.String()
}

// ── Message parsing ───────────────────────────────────────────────────────────

type logMessage struct {
	Role       string                   `json:"role"`
	Content    interface{}              `json:"content"` // string or []part
	ToolCalls  []map[string]interface{} `json:"tool_calls"`
	ToolCallID string                   `json:"tool_call_id"`
}

func parseMessages(rawJSON string) []logMessage {
	if rawJSON == "" {
		return nil
	}
	var req struct {
		Messages []logMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(rawJSON), &req); err != nil {
		return nil
	}
	return req.Messages
}

func writeMessages(b *strings.Builder, msgs []logMessage) {
	if len(msgs) == 0 {
		b.WriteString("_(no messages)_\n\n")
		return
	}
	for i, m := range msgs {
		fmt.Fprintf(b, "### [%d] %s\n\n", i, m.Role)

		switch m.Role {
		case "assistant":
			writeAssistantMessage(b, m)
		case "tool":
			writeToolMessage(b, m)
		default:
			writeContentMessage(b, m)
		}
	}
}

func writeContentMessage(b *strings.Builder, m logMessage) {
	text := contentText(m.Content)
	if text == "" {
		return
	}
	// Truncate very long system prompts — they're rarely relevant to debugging
	if m.Role == "system" && len(text) > 600 {
		fmt.Fprintf(b, "<details><summary>System prompt (%d chars, click to expand)</summary>\n\n```\n%s\n```\n\n</details>\n\n", len(text), text)
		return
	}
	fmt.Fprintf(b, "%s\n\n", text)
}

func writeAssistantMessage(b *strings.Builder, m logMessage) {
	if text := contentText(m.Content); text != "" {
		fmt.Fprintf(b, "%s\n\n", text)
	}
	for _, tc := range m.ToolCalls {
		writeToolCall(b, tc)
	}
}

func writeToolCall(b *strings.Builder, tc map[string]interface{}) {
	id, _ := tc["id"].(string)
	fn, _ := tc["function"].(map[string]interface{})

	name := ""
	args := ""
	if fn != nil {
		name, _ = fn["name"].(string)
		switch a := fn["arguments"].(type) {
		case string:
			if p := prettyJSON(a); p != "" {
				args = p
			} else {
				args = a
			}
		case map[string]interface{}:
			if raw, err := json.MarshalIndent(a, "", "  "); err == nil {
				args = string(raw)
			}
		}
	}

	b.WriteString("**Tool call**\n\n")
	if name != "" {
		fmt.Fprintf(b, "- Function: `%s`\n", name)
	}
	if args != "" {
		// Indent every line so the code block sits cleanly inside the list item
		indented := strings.ReplaceAll(args, "\n", "\n  ")
		fmt.Fprintf(b, "- Arguments:\n  ```json\n  %s\n  ```\n", indented)
	}
	if id != "" {
		fmt.Fprintf(b, "- ID: `%s`\n", id)
	} else {
		b.WriteString("- ID: _(none)_ ⚠ missing tool call ID\n")
	}
	b.WriteString("\n")
}

func writeToolMessage(b *strings.Builder, m logMessage) {
	if m.ToolCallID != "" {
		fmt.Fprintf(b, "- tool_call_id: `%s`\n", m.ToolCallID)
	} else {
		b.WriteString("- tool_call_id: _(none)_ ⚠ missing tool_call_id\n")
	}

	text := contentText(m.Content)
	if text != "" {
		if pretty := prettyJSON(text); pretty != "" {
			fmt.Fprintf(b, "\n```json\n%s\n```\n", pretty)
		} else {
			fmt.Fprintf(b, "\n```\n%s\n```\n", text)
		}
	}
	b.WriteString("\n")
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func contentText(c interface{}) string {
	switch v := c.(type) {
	case string:
		return v
	case []interface{}:
		// Content-parts array: concatenate text parts
		var parts []string
		for _, item := range v {
			if part, ok := item.(map[string]interface{}); ok {
				if t, ok := part["text"].(string); ok {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func prettyJSON(s string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return ""
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(out)
}

// prettyNDJSON pretty-prints each line of a newline-delimited JSON stream.
// Lines that aren't valid JSON are left as-is.
func prettyNDJSON(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		if pretty := prettyJSON(line); pretty != "" {
			out = append(out, pretty)
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
