package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

type onboardingInitRequest struct {
	Tier          string `json:"tier,omitempty"`
	AllowDownload *bool  `json:"allow_download,omitempty"`
}

func handleOnboardingInit(ac *appContext, deps *mcp.ToolDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireOnboardingMutation(ac, w, r) {
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		if len(body) == 0 {
			body = []byte(`{}`)
		}

		var req onboardingInitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
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

		allowDownload := true
		if req.AllowDownload != nil {
			allowDownload = *req.AllowDownload
		}

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, events, runErr := onboarding.RunInit(r.Context(), obDeps, req.Tier, allowDownload)
		for _, ev := range events {
			sseJSON(w, flusher, ev.Type, ev.Data)
		}
		if runErr != nil {
			msg := runErr.Error()
			// Only emit a separate error event if no in-band error was already
			// surfaced (RunInit pushes init_complete on happy paths and
			// returns nil, so any non-nil err is a genuine failure).
			if !hasErrorEvent(events) {
				sseJSON(w, flusher, "error", map[string]any{"message": strings.TrimSpace(msg)})
			}
			return
		}
	}
}

// hasErrorEvent reports whether an event slice already contains an "error"
// record — used so we don't double-report failure to SSE clients.
func hasErrorEvent(events []onboarding.Event) bool {
	for _, ev := range events {
		if ev.Type == "error" {
			return true
		}
	}
	return false
}
