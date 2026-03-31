package openclaw

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type mockBackends struct {
	backends map[string]*Backend
}

func (m *mockBackends) ListBackends() map[string]*Backend { return m.backends }

type mockCatalog struct{}

func (m *mockCatalog) ModelType(name string) string {
	switch name {
	case "qwen3-8b":
		return "llm"
	case "glm-4.1v-9b":
		return "vlm"
	case "qwen3-asr-1.7b":
		return "asr"
	case "qwen3-tts-0.6b":
		return "tts"
	case "z-image":
		return "image_gen"
	default:
		return ""
	}
}

func (m *mockCatalog) ModelContextWindow(name string) int {
	switch name {
	case "qwen3-8b":
		return 32768
	case "glm-4.1v-9b":
		return 8192
	default:
		return 0
	}
}

func (m *mockCatalog) ModelFamily(name string) string {
	switch name {
	case "qwen3-8b", "qwen3-asr-1.7b", "qwen3-tts-0.6b":
		return "qwen"
	case "glm-4.1v-9b":
		return "glm"
	case "z-image":
		return "tongyi"
	default:
		return ""
	}
}

func TestSyncDryRun(t *testing.T) {
	deps := &Deps{
		Backends: &mockBackends{backends: map[string]*Backend{
			"qwen3-8b":       {ModelName: "qwen3-8b", EngineType: "vllm", Address: "http://127.0.0.1:8000", Ready: true},
			"qwen3-asr-1.7b": {ModelName: "qwen3-asr-1.7b", EngineType: "qwen-asr-fastapi", Address: "http://127.0.0.1:8001", Ready: true},
			"qwen3-tts-0.6b": {ModelName: "qwen3-tts-0.6b", EngineType: "qwen-tts-fastapi", Address: "http://127.0.0.1:8002", Ready: true},
			"not-ready":      {ModelName: "not-ready", EngineType: "vllm", Address: "http://127.0.0.1:8003", Ready: false},
			"remote-model":   {ModelName: "remote-model", EngineType: "vllm", Address: "http://192.168.1.5:8000", Ready: true, Remote: true},
		}},
		Catalog:    &mockCatalog{},
		ConfigPath: "/tmp/test-openclaw.json",
		ProxyAddr:  "http://127.0.0.1:6188/v1",
	}

	result, err := Sync(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("Sync dry-run failed: %v", err)
	}

	if result.Written {
		t.Error("dry-run should not write")
	}
	if len(result.LLMModels) != 1 {
		t.Errorf("expected 1 LLM model, got %d", len(result.LLMModels))
	}
	if result.LLMModels[0].ID != "qwen3-8b" {
		t.Errorf("expected LLM model qwen3-8b, got %s", result.LLMModels[0].ID)
	}
	if len(result.LLMModels[0].Input) != 1 || result.LLMModels[0].Input[0] != "text" {
		t.Errorf("expected LLM input [text], got %v", result.LLMModels[0].Input)
	}
	if len(result.ASRModels) != 1 {
		t.Errorf("expected 1 ASR model, got %d", len(result.ASRModels))
	}
	if result.TTSModel == nil || result.TTSModel.ID != "qwen3-tts-0.6b" {
		t.Errorf("expected TTS model qwen3-tts-0.6b, got %v", result.TTSModel)
	}
}

func TestSyncWritesConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "openclaw.json")

	// Pre-populate with existing config (simulating minimax provider)
	existing := map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"minimax": map[string]any{
					"baseUrl": "https://api.minimaxi.com",
					"models":  []any{map[string]any{"id": "MiniMax-M2.1"}},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	os.WriteFile(configPath, data, 0644)

	deps := &Deps{
		Backends: &mockBackends{backends: map[string]*Backend{
			"qwen3-8b": {ModelName: "qwen3-8b", EngineType: "vllm", Address: "http://127.0.0.1:8000", Ready: true},
		}},
		Catalog:    &mockCatalog{},
		ConfigPath: configPath,
		ProxyAddr:  "http://127.0.0.1:6188/v1",
		APIKey:     func() string { return "test-key" },
	}

	result, err := Sync(context.Background(), deps, false)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if !result.Written {
		t.Error("expected Written=true")
	}

	// Read back and verify
	cfg, err := ReadConfig(configPath)
	if err != nil {
		t.Fatalf("ReadConfig failed: %v", err)
	}

	// Verify minimax provider is preserved
	models := cfg["models"].(map[string]any)
	providers := models["providers"].(map[string]any)
	if _, ok := providers["minimax"]; !ok {
		t.Error("minimax provider was removed — merge should preserve non-AIMA providers")
	}

	// Verify AIMA provider was added
	aima, ok := providers["aima"].(map[string]any)
	if !ok {
		t.Fatal("aima provider not found after sync")
	}
	if aima["baseUrl"] != "http://127.0.0.1:6188/v1" {
		t.Errorf("aima baseUrl = %v, want http://127.0.0.1:6188/v1", aima["baseUrl"])
	}
	if aima["apiKey"] != "test-key" {
		t.Errorf("aima apiKey = %v, want test-key", aima["apiKey"])
	}

	managed, err := ReadManagedState(configPath)
	if err != nil {
		t.Fatalf("ReadManagedState failed: %v", err)
	}
	if managed.LLMProvider != "aima" {
		t.Fatalf("managed llm provider = %q, want aima", managed.LLMProvider)
	}
}

func TestMergeAIMAConfigImageGenUsesOpenAIProvider(t *testing.T) {
	merged := MergeAIMAConfig(nil, &SyncResult{
		ImageGenModels: []ImageGenEntry{{ID: "z-image"}},
		ProxyAddr:      "http://127.0.0.1:6188/v1",
	})

	openai := lookupMap(merged, "models", "providers", "openai")
	if openai == nil {
		t.Fatal("openai provider not found after image generation merge")
	}
	if got := openai["baseUrl"]; got != "http://127.0.0.1:6188/v1" {
		t.Fatalf("openai baseUrl = %v, want http://127.0.0.1:6188/v1", got)
	}
	if got := openai["apiKey"]; got != "local" {
		t.Fatalf("openai apiKey = %v, want local", got)
	}
	defaults := lookupMap(merged, "agents", "defaults")
	if defaults == nil {
		t.Fatal("agents.defaults missing after image generation merge")
	}
	imageGen, ok := defaults["imageGenerationModel"].(map[string]any)
	if !ok {
		t.Fatalf("imageGenerationModel = %T, want map", defaults["imageGenerationModel"])
	}
	if got := imageGen["primary"]; got != "openai/z-image" {
		t.Fatalf("imageGenerationModel.primary = %v, want openai/z-image", got)
	}
}

func TestMergeAIMAConfigPreservesUnownedSharedSections(t *testing.T) {
	proxyAddr := "http://127.0.0.1:6188/v1"
	existing := map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"aima": map[string]any{
					"baseUrl": proxyAddr,
					"api":     "openai-completions",
					"models":  []any{map[string]any{"id": "qwen3-8b"}},
				},
				"vllm": map[string]any{
					"baseUrl": proxyAddr,
					"api":     "openai-completions",
					"models":  []any{map[string]any{"id": "qwen3-8b"}},
				},
				"openai": map[string]any{
					"baseUrl": proxyAddr,
					"api":     "openai-completions",
					"models":  []any{map[string]any{"id": "z-image"}},
				},
				"minimax": map[string]any{
					"baseUrl": "https://api.minimax.chat/v1",
					"models":  []any{map[string]any{"id": "MiniMax-M2.1"}},
				},
			},
		},
		"tools": map[string]any{
			"media": map[string]any{
				"audio": map[string]any{
					"enabled": true,
					"models": []any{
						map[string]any{"provider": "openai", "model": "qwen3-asr-1.7b", "baseUrl": proxyAddr},
						map[string]any{"provider": "openai", "model": "whisper-1", "baseUrl": "https://api.openai.com/v1"},
					},
				},
				"image": map[string]any{
					"enabled": true,
					"models": []any{
						map[string]any{"provider": "openai", "model": "glm-4.1v-9b", "baseUrl": proxyAddr},
					},
				},
			},
		},
		"messages": map[string]any{
			"tts": map[string]any{
				"provider": "openai",
				"openai": map[string]any{
					"baseUrl": proxyAddr,
					"model":   "qwen3-tts-0.6b",
					"voice":   "default",
				},
			},
		},
		"env": map[string]any{
			"OPENAI_TTS_BASE_URL": proxyAddr,
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"imageGenerationModel": map[string]any{
					"primary": "openai/z-image",
				},
			},
		},
	}

	merged := MergeAIMAConfig(existing, &SyncResult{ProxyAddr: proxyAddr})
	providers := lookupMap(merged, "models", "providers")
	if providers == nil {
		t.Fatal("models.providers missing after merge")
	}
	if _, ok := providers["minimax"]; !ok {
		t.Fatal("minimax provider should be preserved")
	}
	if _, ok := providers["aima"]; ok {
		t.Fatal("stale aima provider should be removed")
	}
	if _, ok := providers["vllm"]; ok {
		t.Fatal("stale legacy vllm provider should be removed")
	}
	if _, ok := providers["openai"]; !ok {
		t.Fatal("shared openai provider should be preserved without explicit ownership")
	}

	audio := lookupMap(merged, "tools", "media", "audio")
	if audio == nil {
		t.Fatal("tools.media.audio should preserve non-AIMA models")
	}
	audioModels := mediaModels(audio, "https://api.openai.com/v1")
	if len(audioModels) != 1 || audioModels[0] != "whisper-1" {
		t.Fatalf("preserved audio models = %v, want [whisper-1]", audioModels)
	}
	if image := lookupMap(merged, "tools", "media", "image"); image == nil {
		t.Fatal("tools.media.image should be preserved without explicit ownership")
	}
	if messages := lookupMap(merged, "messages"); messages == nil {
		t.Fatal("messages.tts should be preserved without explicit ownership")
	}
	if env := lookupMap(merged, "env"); env == nil {
		t.Fatal("env should be preserved without explicit ownership")
	}
	if defaults := lookupMap(merged, "agents", "defaults"); defaults != nil {
		if _, ok := defaults["imageGenerationModel"]; !ok {
			t.Fatal("imageGenerationModel should be preserved without explicit ownership")
		}
	}
}

func TestMergeAIMAConfigWithManagedStateRemovesOwnedSharedSections(t *testing.T) {
	proxyAddr := "http://127.0.0.1:6188/v1"
	existing := map[string]any{
		"models": map[string]any{
			"providers": map[string]any{
				"openai": map[string]any{
					"baseUrl": proxyAddr,
					"api":     "openai-completions",
					"models":  []any{map[string]any{"id": "z-image"}},
				},
			},
		},
		"tools": map[string]any{
			"media": map[string]any{
				"audio": map[string]any{
					"enabled": true,
					"models": []any{
						map[string]any{"provider": "openai", "model": "qwen3-asr-1.7b", "baseUrl": "https://example.invalid/v1"},
						map[string]any{"provider": "openai", "model": "whisper-1", "baseUrl": "https://api.openai.com/v1"},
					},
				},
				"image": map[string]any{
					"enabled": true,
					"models": []any{
						map[string]any{"provider": "openai", "model": "glm-4.1v-9b", "baseUrl": "https://example.invalid/v1"},
					},
				},
			},
		},
		"messages": map[string]any{
			"tts": map[string]any{
				"provider": "openai",
				"openai": map[string]any{
					"baseUrl": "https://example.invalid/v1",
					"model":   "qwen3-tts-0.6b",
					"voice":   "default",
				},
			},
		},
		"env": map[string]any{
			"OPENAI_TTS_BASE_URL": "https://example.invalid/v1",
		},
		"agents": map[string]any{
			"defaults": map[string]any{
				"imageGenerationModel": map[string]any{
					"primary": "openai/z-image",
				},
			},
		},
	}

	managed := &ManagedState{
		Version:                 managedStateVersion,
		AudioModels:             []string{"qwen3-asr-1.7b"},
		VisionModels:            []string{"glm-4.1v-9b"},
		TTSModel:                "qwen3-tts-0.6b",
		ImageGenerationProvider: "openai",
		ImageGenerationModels:   []string{"z-image"},
	}

	merged, next := MergeAIMAConfigWithState(existing, managed, &SyncResult{ProxyAddr: proxyAddr})
	if next.Empty() == false {
		t.Fatalf("next managed state should be empty after removing all managed sections: %+v", next)
	}
	if providers := lookupMap(merged, "models", "providers"); providers != nil {
		if _, ok := providers["openai"]; ok {
			t.Fatal("managed openai provider should be removed")
		}
	}
	audio := lookupMap(merged, "tools", "media", "audio")
	if audio == nil {
		t.Fatal("audio section should preserve unmanaged models")
	}
	audioModels := mediaModels(audio, "https://api.openai.com/v1")
	if len(audioModels) != 1 || audioModels[0] != "whisper-1" {
		t.Fatalf("preserved audio models = %v, want [whisper-1]", audioModels)
	}
	if image := lookupMap(merged, "tools", "media", "image"); image != nil {
		t.Fatalf("managed image media should be removed: %v", image)
	}
	if messages := lookupMap(merged, "messages"); messages != nil {
		t.Fatalf("managed messages.tts should be removed: %v", messages)
	}
	if env := lookupMap(merged, "env"); env != nil {
		t.Fatalf("managed env should be removed: %v", env)
	}
	if defaults := lookupMap(merged, "agents", "defaults"); defaults != nil {
		if _, ok := defaults["imageGenerationModel"]; ok {
			t.Fatal("managed imageGenerationModel should be removed")
		}
	}
}

func TestSyncVLMInput(t *testing.T) {
	deps := &Deps{
		Backends: &mockBackends{backends: map[string]*Backend{
			"glm-4.1v-9b": {ModelName: "glm-4.1v-9b", EngineType: "vllm", Address: "http://127.0.0.1:8000", Ready: true},
		}},
		Catalog:    &mockCatalog{},
		ConfigPath: "/tmp/test-openclaw.json",
		ProxyAddr:  "http://127.0.0.1:6188/v1",
	}

	result, err := Sync(context.Background(), deps, true)
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}
	if len(result.LLMModels) != 1 {
		t.Fatalf("expected 1 LLM model, got %d", len(result.LLMModels))
	}
	// VLM models should have ["text", "image"] input
	if len(result.LLMModels[0].Input) != 2 || result.LLMModels[0].Input[1] != "image" {
		t.Errorf("VLM input should be [text, image], got %v", result.LLMModels[0].Input)
	}
}

func TestFormatDisplayName(t *testing.T) {
	tests := []struct {
		model, typ, want string
	}{
		{"qwen3-8b", "llm", "Qwen3 8B (AIMA)"},
		{"glm-4.1v-9b", "vlm", "Glm 4.1v 9B (AIMA VLM)"},
		{"qwen3-tts-0.6b", "tts", "Qwen3 Tts 0.6B (AIMA)"},
	}
	for _, tt := range tests {
		got := formatDisplayName(tt.model, tt.typ)
		if got != tt.want {
			t.Errorf("formatDisplayName(%q, %q) = %q, want %q", tt.model, tt.typ, got, tt.want)
		}
	}
}

func TestDefaultMaxTokens(t *testing.T) {
	tests := []struct {
		ctx, want int
	}{
		{0, 4096},
		{2048, 1024},
		{32768, 8192},
		{65536, 8192},
		{8192, 2048},
	}
	for _, tt := range tests {
		got := defaultMaxTokens(tt.ctx)
		if got != tt.want {
			t.Errorf("defaultMaxTokens(%d) = %d, want %d", tt.ctx, got, tt.want)
		}
	}
}
