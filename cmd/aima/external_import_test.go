package main

import (
	"context"
	"testing"

	state "github.com/jguan/aima/internal"
	"github.com/jguan/aima/internal/proxy"
)

func TestRegisterExternalServiceBackends(t *testing.T) {
	proxyServer := proxy.NewServer()
	service := externalServiceOverview{
		BaseURL: "http://127.0.0.1:8004",
		Kind:    "openai",
		Models:  []string{"whisper-large-v3-hf"},
	}

	imported, err := registerExternalServiceBackends(proxyServer, service, nil)
	if err != nil {
		t.Fatalf("registerExternalServiceBackends: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}

	backends := proxyServer.ListBackends()
	backend := backends["whisper-large-v3-hf"]
	if backend == nil {
		t.Fatal("backend for whisper-large-v3-hf missing")
	}
	if !backend.External {
		t.Fatal("External = false, want true")
	}
	if backend.Address != "127.0.0.1:8004" {
		t.Fatalf("Address = %q, want 127.0.0.1:8004", backend.Address)
	}
	if backend.BasePath != "" {
		t.Fatalf("BasePath = %q, want empty", backend.BasePath)
	}
	if backend.Scheme != "http" {
		t.Fatalf("Scheme = %q, want http", backend.Scheme)
	}
	if backend.UpstreamModel != "whisper-large-v3-hf" {
		t.Fatalf("UpstreamModel = %q, want whisper-large-v3-hf", backend.UpstreamModel)
	}
}

func TestRegisterExternalServiceBackendsPreservesHTTPSScheme(t *testing.T) {
	proxyServer := proxy.NewServer()
	service := externalServiceOverview{
		BaseURL: "https://example.com/v1",
		Kind:    "openai",
		Models:  []string{"secure-model"},
	}

	imported, err := registerExternalServiceBackends(proxyServer, service, nil)
	if err != nil {
		t.Fatalf("registerExternalServiceBackends: %v", err)
	}
	if imported != 1 {
		t.Fatalf("imported = %d, want 1", imported)
	}
	backend := proxyServer.ListBackends()["secure-model"]
	if backend == nil {
		t.Fatal("secure-model backend missing")
	}
	if backend.Scheme != "https" {
		t.Fatalf("Scheme = %q, want https", backend.Scheme)
	}
	if backend.Address != "example.com" {
		t.Fatalf("Address = %q, want example.com", backend.Address)
	}
	if backend.BasePath != "" {
		t.Fatalf("BasePath = %q, want empty for /v1 base URL", backend.BasePath)
	}
}

func TestRegisterExternalServiceBackendsRejectsHealthzService(t *testing.T) {
	proxyServer := proxy.NewServer()
	service := externalServiceOverview{
		BaseURL: "http://127.0.0.1:8009",
		Kind:    "healthz",
		Models:  []string{"SenseVoiceSmall", "pyannote"},
	}

	if _, err := registerExternalServiceBackends(proxyServer, service, []string{"local-stt"}); err == nil {
		t.Fatal("registerExternalServiceBackends returned nil, want unsupported service error")
	}
	if backend := proxyServer.ListBackends()["local-stt"]; backend != nil {
		t.Fatalf("local-stt backend should not be registered for healthz service: %+v", backend)
	}
}

func TestRestoreImportedExternalServicesOnlyRestoresReachableScanResults(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { db.Close() })

	for _, svc := range []*state.ExternalService{
		{
			ID:         "external-reachable",
			BaseURL:    "http://127.0.0.1:8004",
			Kind:       "openai",
			Status:     "reachable",
			Source:     "scan",
			ModelsJSON: `["reachable-model"]`,
		},
		{
			ID:         "external-stale",
			BaseURL:    "http://127.0.0.1:8011",
			Kind:       "openai",
			Status:     "reachable",
			Source:     "scan",
			ModelsJSON: `["stale-model"]`,
		},
	} {
		if err := db.UpsertExternalService(ctx, svc); err != nil {
			t.Fatalf("UpsertExternalService: %v", err)
		}
		if err := db.SetExternalServiceImported(ctx, svc.BaseURL, true); err != nil {
			t.Fatalf("SetExternalServiceImported: %v", err)
		}
	}

	proxyServer := proxy.NewServer()
	proxyServer.RegisterBackend("stale-model", &proxy.Backend{
		ModelName: "stale-model",
		Scheme:    "http",
		Address:   "127.0.0.1:8011",
		Ready:     true,
		External:  true,
	})
	proxyServer.RegisterBackend("local-model", &proxy.Backend{
		ModelName: "local-model",
		Address:   "10.42.0.8:8000",
		Ready:     true,
	})
	err = restoreImportedExternalServices(ctx, db, proxyServer, map[string]struct{}{
		"http://127.0.0.1:8004": {},
	})
	if err != nil {
		t.Fatalf("restoreImportedExternalServices: %v", err)
	}

	backends := proxyServer.ListBackends()
	if backends["reachable-model"] == nil {
		t.Fatal("reachable-model backend missing")
	}
	if backends["stale-model"] != nil {
		t.Fatal("stale-model backend should not be restored")
	}
	if backends["local-model"] == nil {
		t.Fatal("local-model backend should not be removed by external stale cleanup")
	}
	services, err := db.ListExternalServices(ctx)
	if err != nil {
		t.Fatalf("ListExternalServices: %v", err)
	}
	for _, svc := range services {
		if svc.BaseURL == "http://127.0.0.1:8011" && svc.Status != "unreachable" {
			t.Fatalf("stale service status = %q, want unreachable", svc.Status)
		}
	}
}
