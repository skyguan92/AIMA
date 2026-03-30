package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSplitCommandLine(t *testing.T) {
	got, err := splitCommandLine(`config set llm.model "qwen 3"`)
	if err != nil {
		t.Fatalf("splitCommandLine returned error: %v", err)
	}

	want := []string{"config", "set", "llm.model", "qwen 3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestExecuteLineDeployUsesRealCLIFlags(t *testing.T) {
	app := testApp(t)

	var (
		gotEngine string
		gotModel  string
		gotSlot   string
		gotConfig map[string]any
	)
	app.ToolDeps.DeployApply = func(ctx context.Context, engine, model, slot string, config map[string]any) (json.RawMessage, error) {
		gotEngine = engine
		gotModel = model
		gotSlot = slot
		gotConfig = config
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	result := ExecuteLine(context.Background(), app, `deploy qwen3-8b --engine llamacpp --slot slot-1 --config gpu_memory_utilization=0.9 --config max_model_len=4096 --max-cold-start 12`, nil)
	if result.ExitCode != 0 {
		t.Fatalf("ExecuteLine exit_code=%d error=%q output=%q", result.ExitCode, result.Error, result.Output)
	}

	if gotEngine != "llamacpp" {
		t.Fatalf("engine = %q, want %q", gotEngine, "llamacpp")
	}
	if gotModel != "qwen3-8b" {
		t.Fatalf("model = %q, want %q", gotModel, "qwen3-8b")
	}
	if gotSlot != "slot-1" {
		t.Fatalf("slot = %q, want %q", gotSlot, "slot-1")
	}
	if gotConfig["gpu_memory_utilization"] != 0.9 {
		t.Fatalf("gpu_memory_utilization = %#v, want 0.9", gotConfig["gpu_memory_utilization"])
	}
	if gotConfig["max_model_len"] != 4096 {
		t.Fatalf("max_model_len = %#v, want 4096", gotConfig["max_model_len"])
	}
	if gotConfig["max_cold_start_s"] != 12 {
		t.Fatalf("max_cold_start_s = %#v, want 12", gotConfig["max_cold_start_s"])
	}
}

func TestExecuteLineConfigGetUsesCLIOutput(t *testing.T) {
	app := testApp(t)
	app.ToolDeps.GetConfig = func(ctx context.Context, key string) (string, error) {
		if key != "llm.model" {
			t.Fatalf("unexpected key %q", key)
		}
		return "qwen3-8b", nil
	}

	result := ExecuteLine(context.Background(), app, `config get llm.model`, nil)
	if result.ExitCode != 0 {
		t.Fatalf("ExecuteLine exit_code=%d error=%q output=%q", result.ExitCode, result.Error, result.Output)
	}
	if result.Output != "qwen3-8b\n" {
		t.Fatalf("output = %q, want %q", result.Output, "qwen3-8b\n")
	}
}
