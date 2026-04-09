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
