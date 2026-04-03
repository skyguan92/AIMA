package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// ShouldIncludeConfigFlag reports whether a resolved config key should be emitted
// as a runtime CLI flag for the given startup command and local model path.
// Some keys, such as quantization, are selection hints for a model artifact rather
// than portable runtime flags across every engine.
func ShouldIncludeConfigFlag(command []string, modelPath, key string, value any) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "":
		return false
	case "quantization":
		return shouldIncludeQuantizationFlag(command, modelPath, value)
	default:
		return true
	}
}

func shouldIncludeQuantizationFlag(command []string, modelPath string, value any) bool {
	if s, ok := value.(string); ok && strings.TrimSpace(s) == "" {
		return false
	}
	if isSingleFileQuantizedModel(modelPath) || commandBakesInModelQuantization(command) {
		return false
	}
	if declared, known := modelConfigDeclaresQuantization(modelPath); known {
		return declared
	}
	return true
}

func isSingleFileQuantizedModel(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gguf", ".ggml":
		return true
	default:
		return false
	}
}

func commandBakesInModelQuantization(command []string) bool {
	for _, arg := range command {
		lower := strings.ToLower(arg)
		base := strings.ToLower(filepath.Base(arg))
		if base == "llama-server" || strings.Contains(lower, "llama_cpp.server") {
			return true
		}
	}
	return false
}

func modelConfigDeclaresQuantization(modelPath string) (declared bool, known bool) {
	if modelPath == "" {
		return false, false
	}
	configDir := modelPath
	if fi, err := os.Stat(modelPath); err == nil && !fi.IsDir() {
		configDir = filepath.Dir(modelPath)
	}
	for _, name := range []string{"config.json", "configuration.json"} {
		data, err := os.ReadFile(filepath.Join(configDir, name))
		if err != nil {
			continue
		}
		var cfg map[string]any
		if err := json.Unmarshal(data, &cfg); err != nil {
			return false, true
		}
		if qc, ok := cfg["quantization_config"].(map[string]any); ok {
			return len(qc) > 0, true
		}
		return false, true
	}
	return false, false
}
