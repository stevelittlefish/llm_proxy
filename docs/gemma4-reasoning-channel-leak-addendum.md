# Addendum: Reasoning-Channel Token Leak

**Relates to:** `gemma4-streaming-tool-call-fix-spec.md`
**Scope:** Same `gemma_4_fix` toggle, same self-contained/removable module as the main spec.

## What it is

A second, separate leak from the same general failure family. In addition to the tool-call markers leaking into `content` (covered in the main spec), Gemma 4's *reasoning/thinking channel* wrapper sometimes leaks into `content` as well — governed by `--reasoning-parser gemma4` rather than `--tool-call-parser gemma4`. Observed form:

```
<|channel>thought
<channel|>
```

## How this differs from the tool-call leak

This one is simpler and does **not** need the buffer/retry/nudge recovery logic from the main spec. The content wrapped between these markers has been observed to always be empty — nothing of value is lost by removing it. The correct handling is pure filtering, not error recovery:

- Detect the markers (`<|channel>thought`, `<channel|>`, and any whitespace/newlines between them).
- Strip them from what's forwarded to the client.
- No retry. No fallback. No branching on whether prior content was already shown — just suppress these specific tokens wherever they appear and let everything else through normally.

## Implementation note

Since this is a streaming proxy, the marker tokens won't always land neatly within a single delta/chunk — the filter needs to tolerate a marker being split across chunk boundaries (e.g., hold a small trailing buffer to check for a partial marker match before forwarding, same general technique as the main spec's buffering, just used here only for stripping rather than for triggering a retry).

## Open item

This addendum assumes the wrapped content is always empty, based on observation so far. If a case ever turns up where there's real reasoning text inside the markers, that would change this from "always-safe to discard" to something needing more thought (e.g., whether to expose it via a `reasoning` field instead of dropping it) — worth a quick sanity check before considering this fully closed, but not expected to change the basic approach.
