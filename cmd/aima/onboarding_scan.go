package main

import (
	"net/http"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// handleOnboardingScan is a thin SSE wrapper around onboarding.RunScan.
// The business logic (parallel engine/model/central sync + event emission)
// lives in the internal/onboarding package so MCP tools and CLI commands can
// share it. Events are streamed in real time via an EventSink — the UI wizard
// sees engine_found / model_found as they happen, not as a post-hoc batch.
func handleOnboardingScan(ac *appContext, deps *mcp.ToolDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireOnboardingMutation(ac, w, r) {
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		stream := newSSEStream(w, flusher)
		stream.startHeartbeat(r.Context(), sseHeartbeatInterval)

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, _, err := onboarding.RunScan(r.Context(), obDeps, stream.sink())
		if err != nil {
			stream.writeJSON("error", map[string]string{"message": err.Error()})
			return
		}
	}
}
