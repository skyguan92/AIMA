package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// onboardingDeployRequest is the JSON body for the onboarding deploy endpoint.
type onboardingDeployRequest struct {
	Model  string `json:"model"`
	Engine string `json:"engine,omitempty"`
}

// handleOnboardingDeploy is the thin SSE wrapper around onboarding.RunDeploy.
// Events are streamed to the client in real time via an EventSink, so the
// wizard UI sees per-step progress (engine_pull, model_pull, deploy) while the
// deployment is happening, not after it has finished.
func handleOnboardingDeploy(ac *appContext, deps *mcp.ToolDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireOnboardingMutation(ac, w, r) {
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		var req onboardingDeployRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, "model is required", http.StatusBadRequest)
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

		var sawError atomic.Bool
		baseSink := stream.sink()
		sink := func(ev onboarding.Event) {
			if ev.Type == "error" {
				sawError.Store(true)
			}
			baseSink(ev)
		}

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, _, runErr := onboarding.RunDeploy(r.Context(), obDeps, req.Model, req.Engine, "", nil, false, sink)
		if runErr != nil && !sawError.Load() {
			stream.writeJSON("error", map[string]any{
				"step":    3,
				"name":    "deploy",
				"message": fmt.Sprintf("%s", runErr),
			})
		}
	}
}
