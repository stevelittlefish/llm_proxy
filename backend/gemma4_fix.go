package backend

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"llm_proxy/models"
)

// This file is a self-contained mitigation for two known bugs in vLLM's
// gemma4 tool-call and reasoning streaming parsers (see
// docs/gemma4-streaming-tool-call-fix-spec.md and the reasoning-channel
// addendum). It is gated entirely behind OpenAIBackend.gemma4FixEnabled
// (config: gemma_4_fix.enabled) and touches no other proxy code path, so it
// can be deleted in its entirety once Gemma 4 is retired from a deployment.

const (
	// Tool-call leak sentinels (main spec, Signal A): Gemma 4's non-JSON
	// tool-call wire format leaking into the content field instead of being
	// converted into a structured tool_calls delta.
	gemma4ToolCallOpenMarker  = "<|tool_call>"
	gemma4ToolCallCloseMarker = "<tool_call|>"
	gemma4ToolCallStringDelim = `<|"|>`

	// Reasoning-channel leak markers (addendum): always observed wrapping
	// empty content, so these are stripped unconditionally with no retry.
	gemma4ChannelOpenMarker  = "<|channel>thought"
	gemma4ChannelCloseMarker = "<channel|>"

	// gemma4HoldbackLen is len(gemma4ChannelOpenMarker)-1, the longest marker
	// above. The content filter holds back at least this many trailing bytes
	// of unmatched content before forwarding it, so a marker split across two
	// SSE chunks is never partially leaked to the client.
	gemma4HoldbackLen = len(gemma4ChannelOpenMarker) - 1

	// gemma4MaxAttempts caps automatic recovery at one original attempt plus
	// two silent retries/nudges, per the spec's retry-limit requirement.
	gemma4MaxAttempts = 3

	// gemma4NudgeText is the internal-only follow-up turn sent after a
	// trailing failure (real content already shown, then a corrupted tail)
	// to prompt the model to actually perform the action it just described.
	gemma4NudgeText = "Go ahead and call the appropriate tool now to do what you just described."

	// gemma4FailureMessage is surfaced to the client only if every recovery
	// attempt is exhausted and the backend is still producing corrupted
	// output; it is always clearly distinguishable from a normal response.
	gemma4FailureMessage = "The backend produced malformed tool-call output and automatic recovery did not succeed."
)

var gemma4ToolCallMarkers = []string{
	gemma4ToolCallOpenMarker,
	gemma4ToolCallCloseMarker,
	gemma4ToolCallStringDelim,
}

// gemma4ContentFilter is a small stateful scanner fed one content delta at a
// time for a single streaming attempt. It strips leaked reasoning-channel
// wrapper tokens unconditionally, and flags (without forwarding) any content
// from the point a tool-call sentinel marker appears onward.
type gemma4ContentFilter struct {
	pending            strings.Builder // tail not yet confirmed safe to forward
	inChannel          bool            // currently inside <|channel>thought ... <channel|>
	suspect            bool            // a tool-call sentinel marker has been observed
	trapped            strings.Builder // raw content withheld once suspect, log-only
	channelLeakSeen    bool            // a reasoning-channel leak was stripped this attempt
	channelLeakContent strings.Builder // content found wrapped inside it, log-only
}

// Feed processes the next content delta and returns only the portion
// confirmed safe to forward to the client right now.
func (f *gemma4ContentFilter) Feed(delta string) string {
	if f.suspect {
		f.trapped.WriteString(delta)
		return ""
	}

	f.pending.WriteString(delta)
	combined := f.pending.String()
	f.pending.Reset()

	var out strings.Builder
	i := 0
	for i < len(combined) {
		if f.inChannel {
			idx := strings.Index(combined[i:], gemma4ChannelCloseMarker)
			if idx < 0 {
				f.pending.WriteString(combined[i:])
				return out.String()
			}
			f.channelLeakContent.WriteString(combined[i : i+idx])
			i += idx + len(gemma4ChannelCloseMarker)
			f.inChannel = false
			continue
		}

		rest := combined[i:]
		matchIdx, matchLen, isChannelOpen := gemma4FindEarliestMarker(rest)

		if matchIdx < 0 {
			// No complete marker ahead. Forward everything except a
			// trailing window that could be the start of a marker split
			// across the next chunk boundary.
			safeLen := len(rest) - gemma4HoldbackLen
			if safeLen < 0 {
				safeLen = 0
			}
			out.WriteString(rest[:safeLen])
			f.pending.WriteString(rest[safeLen:])
			return out.String()
		}

		// Content before the marker is legitimate either way.
		out.WriteString(rest[:matchIdx])

		if isChannelOpen {
			i += matchIdx + matchLen
			f.inChannel = true
			f.channelLeakSeen = true
			continue
		}

		// Tool-call sentinel: flag suspect and trap everything from the
		// marker onward; nothing more is forwarded for this attempt.
		f.suspect = true
		f.trapped.WriteString(rest[matchIdx:])
		return out.String()
	}

	return out.String()
}

// Flush releases any content still held back pending disambiguation of a
// possible split marker. Call once at the end of an attempt, when no further
// deltas are coming so any remaining bytes can't be part of a future split.
func (f *gemma4ContentFilter) Flush() string {
	if f.suspect || f.inChannel {
		return ""
	}
	s := f.pending.String()
	f.pending.Reset()
	return s
}

// Suspect reports whether a tool-call sentinel marker has been observed.
func (f *gemma4ContentFilter) Suspect() bool {
	return f.suspect
}

// Trapped returns the raw content withheld once suspicion was triggered, for
// diagnostic logging only. It must never be forwarded to a client.
func (f *gemma4ContentFilter) Trapped() string {
	return f.trapped.String()
}

// ChannelLeakStripped reports whether a reasoning-channel leak
// (<|channel>thought ... <channel|>) was stripped during this attempt, and
// the content found wrapped inside it. That content is expected to always
// be empty (see the reasoning-channel addendum); a non-empty result is
// log-only but worth investigating, since it would mean real text is being
// silently discarded.
func (f *gemma4ContentFilter) ChannelLeakStripped() (bool, string) {
	return f.channelLeakSeen, f.channelLeakContent.String()
}

// gemma4FindEarliestMarker returns the index and length of whichever known
// marker occurs first (in full) in s, and whether it's the reasoning-channel
// open marker as opposed to one of the tool-call sentinels. matchIdx is -1
// if no marker occurs anywhere in s.
func gemma4FindEarliestMarker(s string) (matchIdx int, matchLen int, isChannelOpen bool) {
	matchIdx = -1

	consider := func(marker string, channelOpen bool) {
		idx := strings.Index(s, marker)
		if idx < 0 {
			return
		}
		if matchIdx == -1 || idx < matchIdx {
			matchIdx = idx
			matchLen = len(marker)
			isChannelOpen = channelOpen
		}
	}

	consider(gemma4ChannelOpenMarker, true)
	for _, m := range gemma4ToolCallMarkers {
		consider(m, false)
	}

	return matchIdx, matchLen, isChannelOpen
}

// gemma4ScanResult summarizes one streaming attempt for the recovery loop in
// handleStreamingChatGemma4Fix.
type gemma4ScanResult struct {
	corrupted           bool
	forwardedText       string // content actually sent to respChan this attempt
	doneReason          string
	usage               *models.OpenAIUsage
	tokenCount          int
	trapped             string // suppressed raw text, log-only
	channelLeakStripped bool   // a reasoning-channel leak was stripped this attempt
	channelLeakContent  string // content found wrapped inside it, log-only
}

// scanGemma4ChatStream reads one SSE response body and forwards content to
// respChan exactly like handleStreamingChat, except every content delta is
// routed through a gemma4ContentFilter first. The moment the filter flags a
// tool-call sentinel leak, forwarding stops for the remainder of this
// attempt (the trapped text is never sent), and the caller decides how to
// recover. Genuine structured tool_calls deltas are accumulated and sent
// exactly as in handleStreamingChat, unaffected by content-leak detection.
func (o *OpenAIBackend) scanGemma4ChatStream(ctx context.Context, body io.Reader, respChan chan<- models.ChatResponse, model string, rawResponse *strings.Builder) gemma4ScanResult {
	scanner := bufio.NewScanner(body)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	filter := &gemma4ContentFilter{}
	tokenCount := 0
	doneReason := "stop"
	var finalUsage *models.OpenAIUsage
	var forwarded strings.Builder

	toolCallsState := make(map[int]struct {
		ID        string
		Name      string
		Arguments string
	})
	toolCallsSent := false
	sendToolCalls := func() {
		if len(toolCallsState) == 0 || toolCallsSent || filter.Suspect() {
			return
		}
		toolCalls := buildToolCallsArray(toolCallsState)
		respChan <- models.ChatResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Message: models.Message{
				Role:      "assistant",
				Content:   "",
				ToolCalls: toolCalls,
			},
			Done: false,
		}
		toolCallsSent = true
	}

	finish := func() gemma4ScanResult {
		if !filter.Suspect() {
			if flushed := filter.Flush(); flushed != "" {
				tokenCount++
				forwarded.WriteString(flushed)
				respChan <- models.ChatResponse{
					Model:     model,
					CreatedAt: time.Now(),
					Message:   models.Message{Role: "assistant", Content: flushed},
					Done:      false,
				}
			}
			sendToolCalls()
		}
		channelLeakStripped, channelLeakContent := filter.ChannelLeakStripped()
		return gemma4ScanResult{
			corrupted:           filter.Suspect(),
			forwardedText:       forwarded.String(),
			doneReason:          doneReason,
			usage:               finalUsage,
			tokenCount:          tokenCount,
			trapped:             filter.Trapped(),
			channelLeakStripped: channelLeakStripped,
			channelLeakContent:  channelLeakContent,
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		rawResponse.WriteString(line)
		rawResponse.WriteString("\n")

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return finish()
		}

		var openaiResp models.OpenAIChatResponse
		if err := json.Unmarshal([]byte(data), &openaiResp); err != nil {
			continue
		}
		if openaiResp.Usage != nil {
			finalUsage = openaiResp.Usage
		}

		if len(openaiResp.Choices) == 0 {
			continue
		}
		choice := openaiResp.Choices[0]

		if choice.FinishReason != "" && choice.FinishReason != "null" {
			doneReason = choice.FinishReason
			sendToolCalls()
			continue
		}

		if choice.Delta == nil {
			continue
		}

		if len(choice.Delta.ToolCalls) > 0 {
			for _, tc := range choice.Delta.ToolCalls {
				tcMap, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}

				index := 0
				if idx, ok := tcMap["index"].(float64); ok {
					index = int(idx)
				}

				if _, exists := toolCallsState[index]; !exists {
					toolCallsState[index] = struct {
						ID        string
						Name      string
						Arguments string
					}{}
				}
				state := toolCallsState[index]

				if id, ok := tcMap["id"].(string); ok && id != "" {
					state.ID = id
				}
				if fn, ok := tcMap["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						state.Name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						state.Arguments += args
					}
				}

				toolCallsState[index] = state
			}
			continue
		}

		if choice.Delta.Content == "" {
			continue
		}

		safe := filter.Feed(choice.Delta.Content)
		if safe == "" {
			continue
		}

		tokenCount++
		forwarded.WriteString(safe)

		role := choice.Delta.Role
		if role == "" {
			role = "assistant"
		}

		select {
		case respChan <- models.ChatResponse{
			Model:     model,
			CreatedAt: time.Now(),
			Message: models.Message{
				Role:     role,
				Content:  safe,
				Thinking: choice.Delta.Thinking,
			},
			Done: false,
		}:
		case <-ctx.Done():
			return finish()
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Scanner error in scanGemma4ChatStream: %v", err)
	}

	return finish()
}

// handleStreamingChatGemma4Fix wraps the normal OpenAI streaming chat
// handling with detection and recovery for the leak described above. It
// preserves live streaming for the overwhelming majority of turns (no
// suspicion ever raised) and only buffers/retries the narrow window where a
// leak is actually detected, per the spec's core design principle.
func (o *OpenAIBackend) handleStreamingChatGemma4Fix(ctx context.Context, firstBody io.Reader, respChan chan<- models.ChatResponse, model string, metadata *BackendMetadata, req models.ChatRequest, convertedMessages []models.Message) {
	startTime := time.Now()
	var rawResponse strings.Builder

	body := firstBody
	messages := convertedMessages
	var pendingProse strings.Builder // forwarded content not yet folded into messages for a nudge
	anyContentForwardedThisTurn := false

	finalTokenCount := 0
	var finalUsage *models.OpenAIUsage
	finalDoneReason := "stop"

	for attempt := 1; attempt <= gemma4MaxAttempts; attempt++ {
		if attempt > 1 {
			rawResponse.WriteString(fmt.Sprintf("\n--- gemma4_fix attempt %d ---\n", attempt))
		}

		result := o.scanGemma4ChatStream(ctx, body, respChan, model, &rawResponse)

		if closer, ok := body.(io.Closer); ok && attempt > 1 {
			closer.Close()
		}

		finalTokenCount = result.tokenCount
		finalUsage = result.usage
		finalDoneReason = result.doneReason

		if result.channelLeakStripped {
			log.Printf("[gemma4_fix] reasoning-channel leak stripped (model=%q, attempt=%d/%d): removed <|channel>thought...<channel|> wrapper, wrapped content=%q",
				model, attempt, gemma4MaxAttempts, result.channelLeakContent)
		}

		if result.forwardedText != "" {
			pendingProse.WriteString(result.forwardedText)
			anyContentForwardedThisTurn = true
		}

		if !result.corrupted {
			metadata.RawResponse = rawResponse.String()
			respChan <- gemma4FinalResponse(model, startTime, finalDoneReason, finalTokenCount, finalUsage)
			return
		}

		log.Printf("[gemma4_fix] tool-call leak detected (model=%q, attempt=%d/%d, finish_reason=%q, content_already_forwarded=%v): suppressed %q",
			model, attempt, gemma4MaxAttempts, result.doneReason, anyContentForwardedThisTurn, result.trapped)

		if attempt == gemma4MaxAttempts {
			break
		}

		var nextMessages []models.Message
		if pendingProse.Len() == 0 {
			// Case A: nothing forwarded yet this turn, safe to discard and
			// retry the byte-identical request.
			nextMessages = messages
		} else {
			// Case B: real content already shown; suppress only the
			// corrupted tail and nudge the model to follow through, rather
			// than risk it re-explaining itself if retried verbatim.
			nextMessages = append(append([]models.Message{}, messages...),
				models.Message{Role: "assistant", Content: pendingProse.String()},
				models.Message{Role: "user", Content: gemma4NudgeText},
			)
			pendingProse.Reset()
		}
		messages = nextMessages

		data, err := o.buildOpenAIChatRequest(req, messages)
		if err != nil {
			log.Printf("[gemma4_fix] failed to build retry request: %v", err)
			break
		}
		metadata.RawRequest += fmt.Sprintf("\n--- gemma4_fix attempt %d request ---\n%s", attempt+1, string(data))

		resp, err := o.postChatCompletion(ctx, data, metadata)
		if err != nil {
			log.Printf("[gemma4_fix] retry request failed: %v", err)
			break
		}
		body = resp.Body
	}

	// Recovery exhausted (or a retry request itself failed): never forward
	// the trapped text, surface a clearly-distinguishable failure instead.
	log.Printf("[gemma4_fix] recovery exhausted for model=%q after %d attempt(s); returning error to client", model, gemma4MaxAttempts)
	metadata.RawResponse = rawResponse.String()
	respChan <- models.ChatResponse{
		Model:      model,
		CreatedAt:  time.Now(),
		Message:    models.Message{Role: "assistant", Content: gemma4FailureMessage},
		Done:       true,
		DoneReason: "error",
	}
}

func gemma4FinalResponse(model string, startTime time.Time, doneReason string, tokenCount int, usage *models.OpenAIUsage) models.ChatResponse {
	totalDuration := time.Since(startTime).Nanoseconds()
	promptTokens := 1
	evalTokens := tokenCount
	if usage != nil {
		promptTokens = usage.PromptTokens
		evalTokens = usage.CompletionTokens
	}
	return models.ChatResponse{
		Model:              model,
		CreatedAt:          time.Now(),
		Message:            models.Message{Role: "assistant", Content: ""},
		Done:               true,
		DoneReason:         doneReason,
		TotalDuration:      totalDuration + 1,
		LoadDuration:       1,
		PromptEvalCount:    promptTokens,
		PromptEvalDuration: 1,
		EvalCount:          evalTokens,
		EvalDuration:       totalDuration,
		Usage:              usage,
	}
}
