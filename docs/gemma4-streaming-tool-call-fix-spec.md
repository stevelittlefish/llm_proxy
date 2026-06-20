# Gemma 4 Streaming Tool-Call Corruption — Proxy Fix Specification

**Audience:** Implementation handoff for Claude Code
**Status:** Design spec, ready for implementation
**Scope:** A fix to be implemented in the existing local LLM proxy that sits between several client applications and a self-hosted vLLM instance serving Gemma 4.

**Note:** This document intentionally does not cover reproduction, test harnesses, or validation strategy — that work is being handled separately by the author. This document covers the problem and the proposed solution design only.

---

## 1. Background

This is a self-hosted local LLM setup. A proxy already exists and sits between multiple downstream client applications and a vLLM server. All traffic — client to proxy, and proxy to vLLM — uses the OpenAI-compatible chat completions API, including streaming.

**Downstream clients currently routed through the proxy:** Home Assistant, Hermes Agent, Pi (coding agent), OpenCode, OpenWebUI, and a custom chat UI.

**Backend:** vLLM serving a quantized `google/gemma-4-31B-it`, launched with `--enable-auto-tool-choice --tool-call-parser gemma4 --reasoning-parser gemma4`.

Gemma 4 uses a custom, non-JSON serialization format for tool calls — not the JSON-based format most other models use:

```
<|tool_call>call:function_name{key:<|"|>value<|"|>,num:42}<tool_call|>
```

Key characteristics of this format:
- The whole call is wrapped between `<|tool_call>` and `<tool_call|>` sentinel tokens.
- Object keys are bare/unquoted.
- String values are delimited by a custom token, `<|"|>`, instead of standard double quotes.
- Multiple tool calls in one turn are concatenated directly with no separator between them.

vLLM's `gemma4` tool-call parser is responsible for recognizing this format as it streams and converting it into the standard OpenAI-compatible `tool_calls` delta structure before any of it reaches a client.

---

## 2. The Problem

### 2.1 Symptom

Intermittently — roughly 1 in 5 tool-call-eligible turns in informal observation, though the exact rate varies — the parser fails to correctly intercept the special tokens during streaming. Instead of being converted into a structured `tool_calls` response, the raw token sequence leaks into the ordinary `content` field as plain text. For example, a client may receive a complete assistant message whose entire content is:

```
<|tool_call>call:read{path:<|"|>usb_comparison_report_updated.html<|"|>}<tool_call|>
```

This is delivered as if it were a normal, finished text response. Critically, the response's `finish_reason` / `stop_reason` comes back as plain `"stop"` rather than the tool-call finish reason — the server itself does not flag that anything went wrong. From the client's point of view, the model appeared to "talk in tokens" instead of doing the thing it was asked to do.

### 2.2 Scope: This Affects Every Downstream Client, Identically

This exact failure mode has been observed, independently, across every client application listed in Section 1 — Home Assistant, Hermes Agent, Pi, OpenCode, OpenWebUI, and the custom chat UI. These are unrelated codebases with no shared tool-call parsing logic of their own. The one thing they all have in common is that they all talk to the same vLLM server through the same OpenAI-compatible streaming API.

This rules out a client-side cause. **The bug is server-side (in vLLM's `gemma4` tool-call parser), and the fix belongs in exactly one place: the proxy.** Fixing it there, once, automatically benefits every current and future downstream client with zero client-side changes.

### 2.3 Root Cause (Upstream Context)

This is a known, actively-being-patched class of bug in vLLM's *streaming* tool-call parsing for Gemma 4 — not something misconfigured in this deployment. It is not unique to vLLM either: a structurally different but comparable bug exists in llama.cpp's own Gemma 4 tool-call parser (a separate PEG-grammar-based implementation). That cross-backend recurrence is a strong signal that Gemma 4's unusual, non-JSON tool-call format is still immature across the serving-engine ecosystem in general, not just in this one stack.

Practical implication: don't expect this to be fully resolved upstream on any predictable timeline, and don't build a fix that only works against today's exact vLLM version or only against vLLM. Favor *detection* over *reconstruction* (see Section 4) so the fix degrades gracefully and keeps working if the backend, model, or vLLM version changes.

### 2.4 Observed Failure Shapes

Two distinct shapes of this failure have been observed in production, and the fix needs to handle both:

1. **Clean failure** — the entire assistant turn consists of nothing but the malformed tool-call text. No legitimate content precedes it. This appears to be the more common case.
2. **Trailing failure** — the model first emits legitimate, correct prose (for example, explaining what it's about to do), and the malformed tool-call text is appended immediately after that prose, within the same turn. This is rarer, but matters a great deal for solution design: by the time corruption is detected, real content may already have been streamed to and displayed by the client. A fix that just "discards and retries the whole turn" would risk that legitimate prose being shown to the user twice (once from the original attempt, once from the retry), which is its own bad UX outcome.

---

## 3. Goals & Non-Goals

**Goals:**
- Detect and recover from this failure mode transparently, for every downstream client, with zero client-side changes required.
- Preserve real-time streaming for the overwhelming majority of turns. This is a hard UX requirement — globally disabling streaming and always using non-streaming requests is explicitly rejected as a solution, since it was tried conversationally and found to make the experience "not fun" (long dead air before any output appears).
- Implement this as a self-contained, toggleable feature — gated behind a single configuration flag such as `gemma_4_fix` — rather than woven generally into the proxy's request handling. This is explicitly a Gemma-4-specific mitigation, not a general-purpose one: Gemma 4 is expected to be a transitional model in this deployment, and the fix should be trivial to disable, and eventually delete outright, once it's retired. It does not need to generalize to other models or backends.
- Fail safe: never surface garbled control-token text to any client under any circumstance.

**Non-goals:**
- Do not attempt to faithfully reconstruct or repair a malformed tool call by parsing its broken syntax (i.e., do not write a full grammar/parser inside the proxy that decodes `call:name{key:<|"|>value<|"|>}` into structured arguments). Detect-and-retry is preferred over decode-and-fix — retrying a short failed generation is simpler and more robust than trying to recover a partially-corrupted payload, and there's no guarantee a corrupted sequence is even fully recoverable by parsing it.
- Do not change vLLM's configuration, version, or attempt to patch vLLM itself. This is a proxy-side mitigation layered in front of an upstream bug, not a fix to the upstream bug.
- Reproduction harness, regression testing, and fix validation are out of scope for this document (handled separately).

---

## 4. Detection Strategy

Two independent, generic signals are used together. Neither requires knowledge of Gemma 4's specific call syntax.

### 4.1 Signal A — Gemma 4 Sentinel Tokens (content-based)

Since this fix is explicitly scoped to Gemma 4 (Section 3) and gated behind its own toggle, detection can use Gemma 4's actual sentinel tokens directly rather than a cross-model heuristic. This is more precise and avoids false positives from unrelated content (e.g., code blocks containing angle brackets or inline HTML). Flag a content delta as suspect when it contains Gemma 4's literal tool-call markers: `<|tool_call>`, `<tool_call|>`, or the string-delimiter token `<|"|>`. This is still pattern matching on the leaked text, not parsing — it identifies that a tool-call attempt leaked into content, without attempting to decode its contents (see Section 3, Non-goals).

### 4.2 Signal B — Protocol-Level Mismatch

The originating request included `tools` (tool-calling was enabled for this turn), and the response's `finish_reason` / `stop_reason` does not correspond to a tool call (e.g., plain `"stop"`) despite the content matching Signal A. This signal is structural — it's available on any OpenAI-compatible backend via a field the server already returns, with no text inspection required at all.

### 4.3 Combining the Signals

- Signal A firing is sufficient to flag a turn as suspect and begin buffering (see Section 5).
- Signal B is only knowable once the turn completes (finish_reason arrives in the final chunk), so it's used as confirmation at turn-end, and as a secondary trigger for cases where content didn't cleanly match Signal A's pattern but the finish reason is still inconsistent with tool-call-shaped content.
- Both signals agreeing should be treated as a definitive corruption event.

---

## 5. Recovery Strategy (Streaming-Preserving)

### 5.1 Core Principle

**Forward every delta live, by default.** Only switch into a cautious buffering mode the moment a chunk looks like it might be the start of a tool call. Once real prose has been flowing for a turn and nothing suspicious has appeared, there's no need to keep scrutinizing the rest of that turn — observed corruption is a wholesale misclassification of a short segment, not something that randomly interrupts an otherwise-normal paragraph mid-stream.

This keeps the fix's cost concentrated on the narrow window where it's actually needed, instead of adding latency to every single response.

### 5.2 Per-Turn State

For each assistant turn, the proxy needs to track, minimally:
- Whether any content has already been forwarded to the client this turn.
- Whether it is currently in a "buffering" state (holding deltas back pending resolution).
- The accumulated buffer of held-back deltas, if buffering.

### 5.3 Flow

1. A new delta arrives from vLLM.
2. If not currently buffering, and the delta does not match Signal A → forward it immediately; mark that content has now been forwarded for this turn.
3. If not currently buffering, and the delta does match Signal A (or vLLM begins emitting a structured `tool_calls` delta that warrants normal handling) → stop forwarding and start buffering instead.
4. While buffering, continue accumulating further deltas without forwarding them downstream.
5. At turn end (a finish_reason is received):
   - **If the buffered segment resolved into a clean, structured tool call** (finish_reason indicates tool use) → release it downstream as a normal `tool_calls` event. From the client's perspective this is indistinguishable from a tool call that streamed normally, aside from a small added delay (the length of the buffered segment, which is typically short).
   - **If Signals A and B together confirm corruption** → proceed to recovery, branching on whether any content was already forwarded this turn (5.4 vs. 5.5).

### 5.4 Case A — Nothing Forwarded Yet This Turn (clean failure)

Nothing has reached the client yet, so it's safe to discard the failed attempt entirely and retry:
- Re-issue an identical request (same messages, tools, and parameters) to vLLM.
- Stream the new attempt to the client as the continuation of the original turn. There is no visible seam — the client simply sees the turn start a little later than it otherwise would have.
- Based on production observations, this is expected to be the more common of the two recovery paths.

### 5.5 Case B — Content Already Forwarded This Turn (trailing failure)

Legitimate prose has already been streamed to and displayed by the client. Re-sending the identical request risks the model re-explaining itself in slightly different words, which would look like duplicated/garbled output to the user — arguably worse than the original bug. Instead:
- Suppress only the corrupted tail. It must never be forwarded to the client.
- Send one additional, minimal follow-up turn "behind the scenes" — not exposed to the client as a literal new chat message, just used internally to drive the next model call — whose purpose is to prompt the model to actually carry out the action it had just described (a short continuation cue is sufficient; exact wording is an open implementation question, see Section 8).
- Stream that follow-up turn's response to the client as a natural continuation of the assistant's turn. An assistant message followed by a tool call is an unremarkable, normal-looking pattern in any chat UI, so this requires no special client-side handling.

### 5.6 Latency Characteristics

- **Unaffected turns (expected majority):** zero added latency — true live, token-by-token streaming, identical to talking to vLLM directly.
- **Clean-failure turns:** added latency is roughly the length of one short failed generation (observed corrupted segments tend to be brief — on the order of a couple dozen tokens) plus the time to start the retry. No full non-streaming round trip is incurred.
- **Trailing-failure turns:** added latency is roughly the length of the corrupted tail plus one short follow-up generation.

---

## 6. Safety & Reliability Constraints

### 6.1 Retry Limits

Automatic retries per logical turn must be capped (for example, at most one or two silent retries) to avoid infinite loops in the event vLLM reliably produces corruption for a particular request shape (rather than corrupting intermittently/randomly).

### 6.2 Fallback Behavior

If retries are exhausted and corruption persists:
- Garbled control-token text must never be surfaced to the client, under any circumstance.
- The proxy should instead return a clearly-formed error/failure response, distinguishable from a normal model response, so client applications and end users aren't left looking at confusing token soup.
- As an optional last-resort step before giving up, a single non-streaming attempt could be used (trading the "stay streaming" preference for at least getting a correct result once normal retries are exhausted) — left as an implementation choice rather than a requirement.

### 6.3 Logging

Every detected corruption event — whether ultimately recovered or not — should be logged with enough detail to diagnose later: a request identifier, timestamp, which signal(s) fired, the raw corrupted content that was suppressed, and whether recovery succeeded. This supports both ongoing operational monitoring of how often this is happening, and separate reproduction/validation work being done outside this document.

---

## 7. Design Constraints (Summary)

- Implement this within the existing proxy codebase, following its existing language, structure, and conventions. This document is intentionally implementation-language-agnostic.
- **Gate the entire feature behind a single configuration flag** (e.g. `gemma_4_fix`). When the flag is off, the proxy must behave exactly as it does today — plain pass-through, no buffering, no detection logic engaged at all.
- **Keep the implementation self-contained** — its own module/file(s), minimal touch points in the core request-handling path — so it can be deleted in its entirety once Gemma 4 is retired from this deployment, without leaving residue in general-purpose proxy code.
- Gemma-4-specific detection (exact sentinel tokens, Section 4.1) is fine and preferred here for precision. What should still be avoided is a full reconstruction/decoding parser for the malformed payload — detect-and-retry remains preferred over decode-and-fix (Section 3, Non-goals).
- No global fallback to non-streaming for all requests; buffering/intervention should be scoped as narrowly as possible (Section 5.1).
- All behavior must be fully transparent to downstream OpenAI-compatible clients: no client-side changes required, and no deviation from the standard OpenAI-compatible streaming response format on the client-facing side of the proxy.

---

## 8. Open Implementation Questions

A few decisions are left open for the implementer:

- **Flag default and location:** whether `gemma_4_fix` defaults to on or off, and where it lives (env var, config file, CLI flag) consistent with how the proxy's other settings are configured.
- **Signal A pattern:** the exact regex/heuristic for "looks like leaked control tokens," balancing false positives (e.g., legitimate text containing angle brackets, such as code blocks with generics or inline HTML) against false negatives.
- **Retry limits:** the exact cap on silent retries and any backoff behavior between attempts.
- **Case B nudge content:** the exact wording/content of the silent continuation cue used to prompt the model forward after a trailing failure.
- **Retry parameter variation:** whether a retried request should vary anything (e.g., sampling seed) to reduce the chance of immediately reproducing the same failure, versus keeping parameters identical for predictability.
- **Metrics/logging detail:** how buffered-but-ultimately-clean tool calls should be distinguished in logs/metrics from ones that actually required recovery, to keep observability signal-to-noise reasonable.

---

## 9. Appendix: Related Upstream Reports (Context Only)

The following are third-party, external reports about Gemma 4 tool-call handling in vLLM and llama.cpp, included purely for background context. They are not dependencies of this fix — the proxy-side mitigation in this document is designed to work regardless of whether or when any of these are resolved upstream.

- vLLM: the `gemma4` tool-call parser corrupting argument values during streaming (value concatenation/corruption observed on boolean fields).
- vLLM: the `gemma4` tool parser producing padding-token-filled output under concurrent request load.
- vLLM: reasoning tokens leaking into `content` in streaming mode for multi-turn conversations that follow a tool result, when using `--reasoning-parser gemma4`.
- vLLM: MTP speculative decoding dropping the first tool call's arguments in streaming, multi-tool-call turns.
- llama.cpp: a structurally distinct but comparable set of Gemma 4 tool-call parsing issues in its PEG-grammar-based parser, including delimiter-token leakage into argument values and a reported "tool call returned as content" failure mode matching the same general symptom described in Section 2.1.
