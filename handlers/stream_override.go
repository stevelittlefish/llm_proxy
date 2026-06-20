package handlers

import "llm_proxy/config"

const (
	streamOverrideAlways = "always"
	streamOverrideNever  = "never"
)

// resolveStream returns the stream flag that should actually be used for the
// request, applying any configured override regardless of what was requested.
func resolveStream(requested bool, cfg *config.Config) bool {
	switch cfg.StreamOverride.Mode {
	case streamOverrideAlways:
		return true
	case streamOverrideNever:
		return false
	default:
		return requested
	}
}
