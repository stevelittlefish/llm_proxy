package models

import (
	"encoding/json"
	"testing"
)

// TestPiAgentContentPartsPayload reproduces a request from the Pi coding agent
// that was rejected with "json: cannot unmarshal array into Go struct field
// Message.messages.content of type string". Pi sends user message content as
// an OpenAI-style content-parts array ([{"type":"text","text":"hi"}]) rather
// than a plain string, which is valid per the OpenAI Chat Completions API.
func TestPiAgentContentPartsPayload(t *testing.T) {
	body := `{"model":"gemma4-31b","messages":[{"role":"system","content":"You are an expert coding assistant operating inside pi, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files.\n\nAvailable tools:\n- read: Read file contents\n- bash: Execute bash commands (ls, grep, find, etc.)\n- edit: Make precise file edits with exact text replacement, including multiple disjoint edits in one call\n- write: Create or overwrite files\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.\n\nGuidelines:\n- Use bash for file operations like ls, rg, find\n- Use read to examine files instead of cat or sed.\n- Use edit for precise changes (edits[].oldText must match exactly)\n- When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls\n- Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.\n- Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.\n- Use write only for new files or complete rewrites.\n- Be concise in your responses\n- Show file paths clearly when working with files\n\nPi documentation (read only when the user asks about pi itself, its SDK, extensions, themes, skills, or TUI):\n- Main documentation: /home/steve/.nvm/versions/node/v24.16.0/lib/node_modules/@earendil-works/pi-coding-agent/README.md\n- Additional docs: /home/steve/.nvm/versions/node/v24.16.0/lib/node_modules/@earendil-works/pi-coding-agent/docs\n- Examples: /home/steve/.nvm/versions/node/v24.16.0/lib/node_modules/@earendil-works/pi-coding-agent/examples (extensions, custom tools, SDK)\n- When reading pi docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pi packages (docs/packages.md)\n- When working on pi topics, read the docs and examples, and follow .md cross-references before implementing\n- Always read pi .md files completely and follow links to related docs (e.g., tui.md for TUI API details)\nCurrent date: 2026-06-20\nCurrent working directory: /home/steve/local_projects/notes"},{"role":"user","content":[{"type":"text","text":"hi"}]}],"stream":true,"stream_options":{"include_usage":true},"store":false,"tools":[{"type":"function","function":{"name":"read","description":"Read the contents of a file. Supports text files and images (jpg, png, gif, webp). Images are sent as attachments. For text files, output is truncated to 2000 lines or 50KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.","parameters":{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},"limit":{"type":"number","description":"Maximum number of lines to read"}}},"strict":false}},{"type":"function","function":{"name":"bash","description":"Execute a bash command in the current working directory. Returns stdout and stderr. Output is truncated to last 2000 lines or 50KB (whichever is hit first). If truncated, full output is saved to a temp file. Optionally provide a timeout in seconds.","parameters":{"type":"object","required":["command"],"properties":{"command":{"type":"string","description":"Bash command to execute"},"timeout":{"type":"number","description":"Timeout in seconds (optional, no default timeout)"}}},"strict":false}},{"type":"function","function":{"name":"edit","description":"Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.","parameters":{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string","description":"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},"newText":{"type":"string","description":"Replacement text for this targeted edit."}},"additionalProperties":false},"description":"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."}},"additionalProperties":false},"strict":false}},{"type":"function","function":{"name":"write","description":"Write content to a file. Creates the file if it doesn't exist, overwrites if it does. Automatically creates parent directories.","parameters":{"type":"object","required":["path","content"],"properties":{"path":{"type":"string","description":"Path to the file to write (relative or absolute)"},"content":{"type":"string","description":"Content to write to the file"}}},"strict":false}}]}`

	var req OpenAIChatRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Fatalf("expected first message role 'system', got %q", req.Messages[0].Role)
	}
	if req.Messages[1].Role != "user" {
		t.Fatalf("expected second message role 'user', got %q", req.Messages[1].Role)
	}
	if req.Messages[1].Content != "hi" {
		t.Fatalf("expected content-parts array to flatten to %q, got %q", "hi", req.Messages[1].Content)
	}
	if !req.Stream {
		t.Fatalf("expected stream to be true")
	}
	if len(req.Tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(req.Tools))
	}

	data, err := json.Marshal(req.Messages[1])
	if err != nil {
		t.Fatalf("marshal message failed: %v", err)
	}
	var remarshal struct {
		Content []map[string]string `json:"content"`
	}
	if err := json.Unmarshal(data, &remarshal); err != nil {
		t.Fatalf("remarshal content was not an array: %v; json=%s", err, string(data))
	}
	if len(remarshal.Content) != 1 || remarshal.Content[0]["type"] != "text" || remarshal.Content[0]["text"] != "hi" {
		t.Fatalf("remarshal content = %#v, want original text part", remarshal.Content)
	}
}

func TestMessagePreservesImageURLContentPart(t *testing.T) {
	body := `{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,abc"}}]}`

	var msg Message
	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if msg.Content != "" {
		t.Fatalf("flattened content = %q, want empty text view", msg.Content)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var got struct {
		Content []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("marshaled content was not preserved as an array: %v; json=%s", err, string(data))
	}
	if len(got.Content) != 1 || got.Content[0].Type != "image_url" || got.Content[0].ImageURL.URL != "data:image/png;base64,abc" {
		t.Fatalf("marshaled content = %#v, want original image_url part", got.Content)
	}
}
