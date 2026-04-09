package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// ExplorerAgentPlanner implements Planner using a tool-calling agent loop.
type ExplorerAgentPlanner struct {
	llm       LLMClient
	workspace *ExplorerWorkspace
	tools     *ExplorerToolExecutor
	queryFn   QueryFunc
	maxCycles int
	maxTasks  int
}

// ExplorerAgentPlannerOption configures the ExplorerAgentPlanner.
type ExplorerAgentPlannerOption func(*ExplorerAgentPlanner)

// WithAgentMaxCycles sets the max PDCA iterations per round.
func WithAgentMaxCycles(n int) ExplorerAgentPlannerOption {
	return func(p *ExplorerAgentPlanner) { p.maxCycles = n }
}

// WithAgentMaxTasks sets the max tasks per plan.
func WithAgentMaxTasks(n int) ExplorerAgentPlannerOption {
	return func(p *ExplorerAgentPlanner) { p.maxTasks = n }
}

// WithAgentQueryFunc sets the knowledge base query function.
func WithAgentQueryFunc(fn QueryFunc) ExplorerAgentPlannerOption {
	return func(p *ExplorerAgentPlanner) { p.queryFn = fn }
}

// NewExplorerAgentPlanner creates a new agent planner.
func NewExplorerAgentPlanner(llm LLMClient, workspace *ExplorerWorkspace, opts ...ExplorerAgentPlannerOption) *ExplorerAgentPlanner {
	p := &ExplorerAgentPlanner{
		llm:       llm,
		workspace: workspace,
		maxCycles: 3,
		maxTasks:  5,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// runPhase executes one agent loop phase (plan, check, or act).
// The LLM reads/writes workspace documents via tool calls until it calls done().
// Returns total tokens used.
func (p *ExplorerAgentPlanner) runPhase(ctx context.Context, phase, systemPrompt string) (int, error) {
	tools := NewExplorerToolExecutor(p.workspace, p.queryFn)
	toolDefs := tools.ToolDefinitions()

	messages := []Message{
		{Role: "system", Content: systemPrompt},
	}

	var totalTokens int
	maxTurns := 30

	for turn := 0; turn < maxTurns; turn++ {
		select {
		case <-ctx.Done():
			return totalTokens, ctx.Err()
		default:
		}

		var resp *Response
		var err error
		if streamer, ok := p.llm.(StreamingLLMClient); ok {
			resp, err = streamer.ChatCompletionStream(ctx, messages, toolDefs, func(delta CompletionDelta) {})
		} else {
			resp, err = p.llm.ChatCompletion(ctx, messages, toolDefs)
		}
		if err != nil {
			return totalTokens, fmt.Errorf("LLM call in %s phase (turn %d): %w", phase, turn, err)
		}
		totalTokens += resp.TotalTokens

		if len(resp.ToolCalls) == 0 {
			slog.Info("explorer agent: phase ended (no tool calls)", "phase", phase, "turn", turn)
			break
		}

		messages = append(messages, Message{
			Role:             "assistant",
			Content:          resp.Content,
			ReasoningContent: resp.ReasoningContent,
			ToolCalls:        resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			slog.Debug("explorer agent: tool call", "phase", phase, "tool", tc.Name)
			result := tools.Execute(tc.Name, json.RawMessage(tc.Arguments))

			content := result.Content
			if result.IsError {
				content = "error: " + content
			}
			messages = append(messages, Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Content:    content,
			})

			if tools.Done() {
				slog.Info("explorer agent: phase done", "phase", phase, "verdict", tools.Verdict(), "turn", turn)
				p.tools = tools
				return totalTokens, nil
			}
		}
	}

	p.tools = tools
	return totalTokens, nil
}
