package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// onboardingDeployRequest is the JSON body for the onboarding deploy endpoint.
type onboardingDeployRequest struct {
	Model  string `json:"model"`
	Engine string `json:"engine,omitempty"`
}

// handleOnboardingDeploy is the thin SSE wrapper around onboarding.RunDeploy.
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

		obDeps := buildOnboardingDepsStruct(ac, deps)
		_, events, runErr := onboarding.RunDeploy(r.Context(), obDeps, req.Model, req.Engine, "", nil, false)
		for _, ev := range events {
			sseJSON(w, flusher, ev.Type, ev.Data)
		}
		if runErr != nil && !hasErrorEvent(events) {
			sseJSON(w, flusher, "error", map[string]any{
				"step":    3,
				"name":    "deploy",
				"message": fmt.Sprintf("%s", runErr),
			})
		}
	}
}
