package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jguan/aima/internal/knowledge"
)

func TestApplyWarmupReadinessRequiresSuccessfulWarmup(t *testing.T) {
	var calls int32
	gotModels := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		defer r.Body.Close()
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotModels <- payload.Model
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"warmup"}`))
	}))
	defer srv.Close()

	asset := &knowledge.EngineAsset{}
	asset.Startup.Warmup = knowledge.WarmupConfig{Enabled: true, Prompt: "Hello", MaxTokens: 1, TimeoutS: 1}
	cache := newWarmupReadyCache()
	address := strings.TrimPrefix(srv.URL, "http://")

	first := &DeploymentStatus{
		Name:    "demo",
		Model:   "demo-model",
		Ready:   true,
		Address: address,
		Labels:  map[string]string{servedModelLabel: "served-demo"},
	}
	applyWarmupReadiness(context.Background(), first, asset, cache)
	if first.Ready {
		t.Fatal("deployment should remain not ready until warmup succeeds")
	}
	if first.StartupPhase != "warmup" {
		t.Fatalf("startup phase = %q, want warmup", first.StartupPhase)
	}
	if first.StartupProgress != 95 {
		t.Fatalf("startup progress = %d, want 95", first.StartupProgress)
	}
	select {
	case model := <-gotModels:
		if model != "served-demo" {
			t.Fatalf("warmup model = %q, want served-demo", model)
		}
	case <-time.After(time.Second):
		t.Fatal("warmup request was not observed")
	}

	second := &DeploymentStatus{
		Name:    "demo",
		Model:   "demo-model",
		Ready:   true,
		Address: address,
		Labels:  map[string]string{servedModelLabel: "served-demo"},
	}
	applyWarmupReadiness(context.Background(), second, asset, cache)
	if !second.Ready {
		t.Fatal("deployment should become ready after successful warmup")
	}
	if !cache.Has("demo") {
		t.Fatal("successful warmup should be cached")
	}
	select {
	case model := <-gotModels:
		if model != "served-demo" {
			t.Fatalf("warmup model = %q, want served-demo", model)
		}
	case <-time.After(time.Second):
		t.Fatal("second warmup request was not observed")
	}

	third := &DeploymentStatus{
		Name:    "demo",
		Model:   "demo-model",
		Ready:   true,
		Address: address,
		Labels:  map[string]string{servedModelLabel: "served-demo"},
	}
	applyWarmupReadiness(context.Background(), third, asset, cache)
	if !third.Ready {
		t.Fatal("cached warmup readiness should keep deployment ready")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("warmup call count = %d, want 2", got)
	}
}

func TestApplyWarmupReadinessSkipsDisabledWarmup(t *testing.T) {
	ds := &DeploymentStatus{
		Name:    "demo",
		Model:   "demo-model",
		Ready:   true,
		Address: "127.0.0.1:8000",
	}
	asset := &knowledge.EngineAsset{}
	applyWarmupReadiness(context.Background(), ds, asset, newWarmupReadyCache())
	if !ds.Ready {
		t.Fatal("disabled warmup should not change readiness")
	}
}

func TestDeploymentServedModelFallsBackFromTemplateLabel(t *testing.T) {
	ds := &DeploymentStatus{
		Model: "GLM-4.1V-9B-Thinking-FP4",
		Labels: map[string]string{
			servedModelLabel: "{{.ModelName}}",
			"aima.dev/model": "GLM-4.1V-9B-Thinking-FP4",
		},
	}
	if got := deploymentServedModel(ds); got != "GLM-4.1V-9B-Thinking-FP4" {
		t.Fatalf("deploymentServedModel = %q, want model label fallback", got)
	}
}
