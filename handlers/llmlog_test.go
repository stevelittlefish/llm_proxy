package handlers

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"llm_proxy/database"
)

func TestLogContentSummarizesImageParts(t *testing.T) {
	// 1x1 PNG data URI.
	raw := `{"model":"vision","messages":[{"role":"user","content":[{"type":"text","text":"what is this?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="}}]}]}`

	last := lastMessageSummaryFromRaw(raw)
	for _, needle := range []string{"what is this?", "[image: image/png", "1×1"} {
		if !strings.Contains(last, needle) {
			t.Fatalf("last summary = %q, want %q", last, needle)
		}
	}

	entry := database.LogEntry{FrontendRequest: raw, LastMessage: ""}
	view := makeLogListEntry(entry)
	if view.Preview == "" || len(view.PreviewParts) != 2 || view.PreviewParts[1].Kind != "image" || view.PreviewParts[1].URL == "" {
		t.Fatalf("view = %#v, want image preview part", view)
	}
	tmpl := template.Must(template.New("img").Parse(`<img src="{{.URL}}">`))
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, view.PreviewParts[1]); err != nil {
		t.Fatalf("template execute failed: %v", err)
	}
	if strings.Contains(rendered.String(), "#ZgotmplZ") || !strings.Contains(rendered.String(), "data:image/png;base64") {
		t.Fatalf("template rendered image URL unsafely: %s", rendered.String())
	}

	md := formatLLMLog(&database.LogEntry{ID: 1, Timestamp: time.Now(), FrontendRequest: raw})
	if !strings.Contains(md, "[image: image/png") {
		t.Fatalf("markdown did not include image summary:\n%s", md)
	}
}

func TestFormatLLMLog_SampleRequest(t *testing.T) {
	feReq := `{"model":"gemma4-31b","messages":[{"role":"system","content":"You are Nabu."},{"role":"user","content":"Are you alive?"},{"role":"assistant","content":"No, I am a conversational AI assistant."},{"role":"user","content":"Good enough. Can you activate lamps on Office?"},{"role":"assistant","tool_calls":[{"function":{"name":"HassTurnOn","arguments":{"area":"Office","name":"Office Lamps"}}}]},{"role":"tool","content":"{\"response_type\":\"action_done\"}"}]}`

	beReq := `{"model":"gemma4-31b","messages":[{"role":"system","content":"You are Nabu."},{"role":"user","content":"Are you alive?"},{"role":"assistant","content":"No, I am a conversational AI assistant."},{"role":"user","content":"Good enough. Can you activate lamps on Office?"},{"role":"assistant","content":"","tool_calls":[{"function":{"arguments":"{\"area\":\"Office\",\"name\":\"Office Lamps\"}","name":"HassTurnOn"},"id":"call-cefbbf7f40f12e9f","type":"function"}]},{"role":"tool","content":"{\"response_type\":\"action_done\"}"}]}`

	entry := &database.LogEntry{
		ID:              2181,
		Timestamp:       time.Date(2026, 5, 24, 0, 42, 51, 0, time.UTC),
		Endpoint:        "/api/chat",
		Model:           "gemma4-31b",
		StatusCode:      500,
		LatencyMs:       18,
		Stream:          true,
		BackendType:     "openai",
		Error:           `can only concatenate str (not "NoneType") to str (BadRequestError)`,
		FrontendURL:     "http://0.0.0.0:11434/api/chat",
		BackendURL:      "http://vllm-server:8000/v1/chat/completions",
		FrontendRequest: feReq,
		BackendRequest:  beReq,
	}

	out := formatLLMLog(entry)
	t.Log("\n" + out)

	for _, needle := range []string{
		"# LLM Proxy Request #2181",
		"gemma4-31b",
		"500",
		"BadRequestError",
		"HassTurnOn",
		"⚠ missing tool call ID", // frontend tool call has no ID
		"⚠ missing tool_call_id", // tool result has no tool_call_id
		"call-cefbbf7f40f12e9f",  // backend has the ID
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("expected output to contain %q", needle)
		}
	}
}
