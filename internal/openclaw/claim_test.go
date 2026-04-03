package openclaw

import (
	"context"
	"testing"
)

func TestClaimDryRunDetectsLegacySharedConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeLegacyClaimableConfig(t, tmpDir, "http://127.0.0.1:6188/v1")

	result, err := Claim(context.Background(), legacyClaimDeps(configPath), ClaimOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Claim dry-run failed: %v", err)
	}
	if result.Written {
		t.Fatal("dry-run should not write managed state")
	}
	if got := result.Detected.TTSModel; got != "qwen3-tts-0.6b" {
		t.Fatalf("detected tts = %q, want qwen3-tts-0.6b", got)
	}
	if got := result.Claimed.ImageGenModels; len(got) != 1 || got[0] != "z-image" {
		t.Fatalf("claimed image gen = %v, want [z-image]", got)
	}
}

func TestClaimWritesManagedStateForRequestedSections(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeLegacyClaimableConfig(t, tmpDir, "http://127.0.0.1:6188/v1")

	result, err := Claim(context.Background(), legacyClaimDeps(configPath), ClaimOptions{
		Sections: []string{"asr", "tts"},
	})
	if err != nil {
		t.Fatalf("Claim failed: %v", err)
	}
	if !result.Written {
		t.Fatal("claim should write managed state")
	}

	managed, err := ReadManagedState(configPath)
	if err != nil {
		t.Fatalf("ReadManagedState failed: %v", err)
	}
	if len(managed.AudioModels) != 1 || managed.AudioModels[0] != "qwen3-asr-1.7b" {
		t.Fatalf("managed audio = %v, want [qwen3-asr-1.7b]", managed.AudioModels)
	}
	if managed.TTSModel != "qwen3-tts-0.6b" {
		t.Fatalf("managed tts = %q, want qwen3-tts-0.6b", managed.TTSModel)
	}
	if len(managed.VisionModels) != 0 || managed.ImageGenerationProvider != "" || managed.LLMProvider != "" {
		t.Fatalf("managed state should only claim requested sections: %+v", managed)
	}
}

func TestClaimIgnoresUnexpectedLocalProxyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeLegacyClaimableConfig(t, tmpDir, "http://127.0.0.1:6188/v1")

	result, err := Claim(context.Background(), &Deps{
		Backends:   &mockBackends{backends: map[string]*Backend{}},
		Catalog:    &mockCatalog{},
		ConfigPath: configPath,
		ProxyAddr:  "http://127.0.0.1:6188/v1",
	}, ClaimOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Claim dry-run failed: %v", err)
	}
	if summaryCount(result.Claimed) != 0 {
		t.Fatalf("claimed = %+v, want empty when AIMA does not currently expect these models", result.Claimed)
	}
}

func legacyClaimDeps(configPath string) *Deps {
	return &Deps{
		Backends: &mockBackends{backends: map[string]*Backend{
			"qwen3-asr-1.7b": {ModelName: "qwen3-asr-1.7b", Ready: true},
			"qwen3-tts-0.6b": {ModelName: "qwen3-tts-0.6b", Ready: true},
			"z-image":        {ModelName: "z-image", Ready: true},
		}},
		Catalog:    &mockCatalog{},
		ConfigPath: configPath,
		ProxyAddr:  "http://127.0.0.1:6188/v1",
	}
}

func writeLegacyClaimableConfig(t *testing.T, dir, proxyAddr string) string {
	t.Helper()

	configPath := dir + "/openclaw.json"
	cfg := map[string]any{
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
						map[string]any{"provider": "openai", "model": "qwen3-asr-1.7b", "baseUrl": proxyAddr},
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
	if err := WriteConfig(configPath, cfg); err != nil {
		t.Fatalf("WriteConfig failed: %v", err)
	}
	return configPath
}
