package agent

import (
	"context"
	"encoding/json"
	"testing"
)

// mockStreamingLLM is a test double that returns pre-scripted responses.
type mockStreamingLLM struct {
	responses []Response
	callIndex int
	calls     [][]Message
}

func (m *mockStreamingLLM) ChatCompletion(ctx context.Context, messages []Message, tools []ToolDefinition) (*Response, error) {
	m.calls = append(m.calls, messages)
	if m.callIndex >= len(m.responses) {
		return &Response{Content: ""}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return &resp, nil
}

func (m *mockStreamingLLM) ChatCompletionStream(ctx context.Context, messages []Message, tools []ToolDefinition, onDelta func(CompletionDelta)) (*Response, error) {
	return m.ChatCompletion(ctx, messages, tools)
}

func TestRunPhase_PlanWritesAndDone(t *testing.T) {
	dir := t.TempDir()
	ws := NewExplorerWorkspace(dir)
	_ = ws.Init()

	planContent := `# Exploration Plan

## Strategy
Test combo.

## Tasks
` + "```yaml\n" + `- kind: validate
  model: test-model
  engine: vllm
  engine_params:
    gpu_memory_utilization: 0.90
  benchmark:
    concurrency: [1]
    input_tokens: [128]
    max_tokens: [256]
    requests_per_combo: 3
  reason: "test"
` + "```\n"

	mock := &mockStreamingLLM{
		responses: []Response{
			{ToolCalls: []ToolCall{
				{ID: "1", Name: "cat", Arguments: `{"path":"device-profile.md"}`},
			}},
			{ToolCalls: []ToolCall{
				{ID: "2", Name: "write", Arguments: `{"path":"plan.md","content":` + jsonEscape(planContent) + `}`},
			}},
			{ToolCalls: []ToolCall{
				{ID: "3", Name: "done", Arguments: `{}`},
			}},
		},
	}

	_ = ws.writeFactDocument("device-profile.md", "# Device\n## Hardware\n- GPU: test\n")

	planner := &ExplorerAgentPlanner{
		llm:       mock,
		workspace: ws,
		maxTasks:  5,
		maxCycles: 3,
	}

	tokens, err := planner.runPhase(context.Background(), "plan", "test system prompt")
	if err != nil {
		t.Fatalf("runPhase: %v", err)
	}
	if tokens < 0 {
		t.Errorf("tokens=%d", tokens)
	}

	tasks, err := ws.ParsePlan()
	if err != nil {
		t.Fatalf("ParsePlan: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Model != "test-model" {
		t.Errorf("tasks: %+v", tasks)
	}
}

// jsonEscape returns a JSON string literal for content.
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAgentPlannerPlan(t *testing.T) {
	dir := t.TempDir()
	ws := NewExplorerWorkspace(dir)
	_ = ws.Init()

	planYAML := `- kind: validate
  model: test-model
  engine: vllm
  engine_params:
    gpu_memory_utilization: 0.90
  benchmark:
    concurrency: [1]
    input_tokens: [128]
    max_tokens: [256]
    requests_per_combo: 3
  reason: "test"`

	planContent := "# Exploration Plan\n\n## Strategy\nTest.\n\n## Tasks\n```yaml\n" + planYAML + "\n```\n"

	mock := &mockStreamingLLM{
		responses: []Response{
			{ToolCalls: []ToolCall{{ID: "1", Name: "cat", Arguments: `{"path":"device-profile.md"}`}}},
			{ToolCalls: []ToolCall{{ID: "2", Name: "write", Arguments: `{"path":"plan.md","content":` + jsonEscape(planContent) + `}`}}},
			{ToolCalls: []ToolCall{{ID: "3", Name: "done", Arguments: `{}`}}},
		},
	}

	input := PlanInput{
		Hardware:     HardwareInfo{Profile: "nvidia-rtx4090-x86", GPUArch: "Ada", GPUCount: 2, VRAMMiB: 49140},
		LocalModels:  []LocalModel{{Name: "test-model", Format: "safetensors", Type: "llm", SizeBytes: 5_000_000_000}},
		LocalEngines: []LocalEngine{{Name: "vllm", Type: "vllm", Runtime: "container"}},
	}

	planner := NewExplorerAgentPlanner(mock, ws)
	plan, tokens, err := planner.Plan(context.Background(), input)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if tokens < 0 {
		t.Logf("tokens=%d (mock returns 0, ok)", tokens)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(plan.Tasks))
	}
	if plan.Tasks[0].Model != "test-model" {
		t.Errorf("task model=%s", plan.Tasks[0].Model)
	}
	if plan.Tier != 2 {
		t.Errorf("tier=%d", plan.Tier)
	}
}

func TestAgentPlannerAnalyze(t *testing.T) {
	dir := t.TempDir()
	ws := NewExplorerWorkspace(dir)
	_ = ws.Init()

	summaryContent := `# Exploration Summary

## Key Findings
- vllm works well

## Recommended Configurations
` + "```yaml\n" + `- model: test-model
  engine: vllm
  hardware: nvidia-rtx4090-x86
  engine_params: {}
  performance:
    throughput_tps: 100.0
    latency_p50_ms: 40
  confidence: validated
  note: "good"
` + "```\n" + `
## Current Strategy
Done for now.
`

	mock := &mockStreamingLLM{
		responses: []Response{
			{ToolCalls: []ToolCall{{ID: "1", Name: "cat", Arguments: `{"path":"experiments/001-test-model-vllm.md"}`}}},
			{ToolCalls: []ToolCall{{ID: "2", Name: "write", Arguments: `{"path":"summary.md","content":` + jsonEscape(summaryContent) + `}`}}},
			{ToolCalls: []ToolCall{{ID: "3", Name: "done", Arguments: `{"verdict":"done"}`}}},
		},
	}

	_ = ws.writeFactDocument("experiments/001-test-model-vllm.md", "# Experiment\n## Result\nstatus: completed\n")

	planner := NewExplorerAgentPlanner(mock, ws)
	verdict, extraTasks, _, err := planner.Analyze(context.Background())
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if verdict != "done" {
		t.Errorf("verdict=%s", verdict)
	}
	if len(extraTasks) != 0 {
		t.Errorf("extraTasks=%d (expected 0 for verdict=done)", len(extraTasks))
	}
}
