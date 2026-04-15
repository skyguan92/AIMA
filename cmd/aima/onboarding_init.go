package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

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

		stream := newSSEStream(w, flusher)
		stream.startHeartbeat(r.Context(), sseHeartbeatInterval)

		var sawError atomic.Bool
		baseSink := stream.sink()
		sink := func(ev onboarding.Event) {
			if ev.Type == "error" {
				sawError.Store(true)
			}
			baseSink(ev)
		}

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, _, runErr := onboarding.RunInit(r.Context(), obDeps, req.Tier, allowDownload, sink)
		if runErr != nil && !sawError.Load() {
			stream.writeJSON("error", map[string]any{"message": strings.TrimSpace(runErr.Error())})
		}
	}
}
