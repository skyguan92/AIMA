package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// sseWrite sends a single SSE event with a raw data string.
func sseWrite(w http.ResponseWriter, f http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	f.Flush()
}

// sseJSON sends a single SSE event with a JSON-marshaled payload.
func sseJSON(w http.ResponseWriter, f http.Flusher, event string, v any) {
	b, _ := json.Marshal(v)
	sseWrite(w, f, event, string(b))
}

// sseEventSink returns an onboarding.EventSink that writes every event to the
// SSE response in real time. A mutex serializes concurrent emits coming from
// the parallel goroutines inside RunScan / DeployRun.
func sseEventSink(w http.ResponseWriter, f http.Flusher) onboarding.EventSink {
	var mu sync.Mutex
	return func(ev onboarding.Event) {
		mu.Lock()
		defer mu.Unlock()
		sseJSON(w, f, ev.Type, ev.Data)
	}
}

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

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, _, err := onboarding.RunScan(r.Context(), obDeps, sseEventSink(w, flusher))
		if err != nil {
			sseJSON(w, flusher, "error", map[string]string{"message": err.Error()})
			return
		}
	}
}
