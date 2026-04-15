package main

import (
	"encoding/json"
	"fmt"
	"net/http"

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

// handleOnboardingScan is a thin SSE wrapper around onboarding.RunScan.
// The business logic (parallel engine/model/central sync + event collection)
// lives in the internal/onboarding package so MCP tools and CLI commands can
// share it.
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
		_, events, err := onboarding.RunScan(r.Context(), obDeps)
		for _, ev := range events {
			if ev.Type == "scan_complete" {
				// final payload handled below to preserve legacy shape
				continue
			}
			sseJSON(w, flusher, ev.Type, ev.Data)
		}
		if err != nil {
			sseJSON(w, flusher, "error", map[string]string{"message": err.Error()})
			return
		}
		// Replay scan_complete with the canonical payload shape.
		for _, ev := range events {
			if ev.Type == "scan_complete" {
				sseJSON(w, flusher, ev.Type, ev.Data)
			}
		}
	}
}
