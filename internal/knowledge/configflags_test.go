package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShouldIncludeConfigFlag(t *testing.T) {
	t.Run("skip quantization for gguf llama server", func(t *testing.T) {
		if ShouldIncludeConfigFlag(
			[]string{"llama-server", "--model", "{{.ModelPath}}"},
			"/models/qwen3/Qwen3-4B-Q4_K_M.gguf",
			"quantization",
			"int4",
		) {
			t.Fatal("quantization flag should be omitted for llama.cpp GGUF deployments")
		}
	})

	t.Run("skip quantization when local config has no quantization metadata", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"model_type":"qwen3","torch_dtype":"bfloat16"}`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if ShouldIncludeConfigFlag(
			[]string{"vllm", "serve", "{{.ModelPath}}"},
			dir,
			"quantization",
			"gptq",
		) {
			t.Fatal("quantization flag should be omitted when config.json does not declare quantization_config")
		}
	})

	t.Run("keep quantization when local config declares quantization metadata", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"quantization_config":{"quant_method":"gptq","bits":4}}`), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
		if !ShouldIncludeConfigFlag(
			[]string{"vllm", "serve", "{{.ModelPath}}"},
			dir,
			"quantization",
			"gptq",
		) {
			t.Fatal("quantization flag should be kept when config.json declares quantization_config")
		}
	})
}
