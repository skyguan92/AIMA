package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/proxy"
)

func TestHandleOnboardingUseExistingSetsLLMConfig(t *testing.T) {
	proxyServer := proxy.NewServer()
	proxyServer.RegisterBackend("qwen3.6-35b-a3b-bf16", &proxy.Backend{
		ModelName:     "qwen3.6-35b-a3b-bf16",
		EngineType:    "vllm",
		Address:       "127.0.0.1:18310",
		BasePath:      "/v1",
		Ready:         true,
		UpstreamModel: "qwen3.6-35b-a3b-bf16",
	})

	config := map[string]string{}
	handler := handleOnboardingUseExisting(&appContext{proxy: proxyServer}, &mcp.ToolDeps{
		SetConfig: func(ctx context.Context, key, value string) error {
			config[key] = value
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/ui/api/onboarding-use-existing", strings.NewReader(`{"model":"qwen3.6-35b-a3b-bf16"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := config["llm.endpoint"]; got != "http://127.0.0.1:18310/v1" {
		t.Fatalf("llm.endpoint = %q", got)
	}
	if got := config["llm.model"]; got != "qwen3.6-35b-a3b-bf16" {
		t.Fatalf("llm.model = %q", got)
	}
	if got := config["onboarding_completed"]; got != "true" {
		t.Fatalf("onboarding_completed = %q", got)
	}
}
