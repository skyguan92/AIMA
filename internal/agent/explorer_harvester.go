package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// HarvestInput contains the exploration task result for post-processing.
type HarvestInput struct {
	Task   PlanTask
	Result HarvestResult
}

// HarvestResult captures benchmark/exploration outcomes.
type HarvestResult struct {
	Success    bool
	Throughput float64
	TTFTP95    float64
	VRAMMiB    float64
	Config     map[string]any
	Promoted   bool // set by maybeAutoPromote
	Error      string
}

// HarvestAction describes a post-exploration side effect.
type HarvestAction struct {
	Type   string // "promote", "note", "sync_push", "update_question", "feedback"
	Detail string
}

// Harvester collects exploration results and performs post-processing.
type Harvester struct {
	tier     int
	llm      LLMClient // nil for Tier 1
	syncPush func(ctx context.Context) error
	saveNote func(ctx context.Context, title, content, hardware, model, engine string) error
}

type HarvesterOption func(*Harvester)

func WithHarvesterLLM(llm LLMClient) HarvesterOption {
	return func(h *Harvester) { h.llm = llm }
}

func WithSyncPush(fn func(ctx context.Context) error) HarvesterOption {
	return func(h *Harvester) { h.syncPush = fn }
}

func WithSaveNote(fn func(ctx context.Context, title, content, hardware, model, engine string) error) HarvesterOption {
	return func(h *Harvester) { h.saveNote = fn }
}

func NewHarvester(tier int, opts ...HarvesterOption) *Harvester {
	h := &Harvester{tier: tier}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Harvest processes an exploration result and returns actions taken.
func (h *Harvester) Harvest(ctx context.Context, input HarvestInput) []HarvestAction {
	var actions []HarvestAction

	if !input.Result.Success {
		note := fmt.Sprintf("%s on %s: FAILED -- %s", input.Task.Model, input.Task.Engine, input.Result.Error)
		actions = append(actions, HarvestAction{Type: "note", Detail: note})
		if h.saveNote != nil {
			_ = h.saveNote(ctx, "exploration failed", note, input.Task.Hardware, input.Task.Model, input.Task.Engine)
		}
		return actions
	}

	// Record knowledge note
	note := h.generateNote(ctx, input)
	actions = append(actions, HarvestAction{Type: "note", Detail: note})
	if h.saveNote != nil {
		title := fmt.Sprintf("%s on %s benchmark", input.Task.Model, input.Task.Engine)
		_ = h.saveNote(ctx, title, note, input.Task.Hardware, input.Task.Model, input.Task.Engine)
	}

	// Track promotion
	if input.Result.Promoted {
		actions = append(actions, HarvestAction{
			Type:   "promote",
			Detail: fmt.Sprintf("%s promoted to golden", input.Task.Model),
		})
	}

	// Sync push if available
	if h.syncPush != nil {
		if err := h.syncPush(ctx); err != nil {
			slog.Warn("harvester sync push failed", "error", err)
		} else {
			actions = append(actions, HarvestAction{Type: "sync_push", Detail: "incremental push"})
		}
	}

	return actions
}

func (h *Harvester) generateNote(ctx context.Context, input HarvestInput) string {
	if h.tier >= 2 && h.llm != nil {
		note, err := h.generateLLMNote(ctx, input)
		if err == nil {
			return note
		}
		slog.Warn("LLM note generation failed, falling back to template", "error", err)
	}
	return h.generateTemplateNote(input)
}

func (h *Harvester) generateTemplateNote(input HarvestInput) string {
	return fmt.Sprintf("%s on %s: %.1f tok/s, TTFT P95 %.0fms, config=%v",
		input.Task.Model, input.Task.Engine,
		input.Result.Throughput, input.Result.TTFTP95,
		input.Result.Config)
}

func (h *Harvester) generateLLMNote(ctx context.Context, input HarvestInput) (string, error) {
	if h.llm == nil {
		return "", fmt.Errorf("no LLM client")
	}
	prompt := fmt.Sprintf(
		"Summarize this benchmark result in 2-3 sentences with actionable insights:\n"+
			"Model: %s, Engine: %s\n"+
			"Throughput: %.1f tok/s, TTFT P95: %.0fms, VRAM: %.0f MiB\n"+
			"Config: %v",
		input.Task.Model, input.Task.Engine,
		input.Result.Throughput, input.Result.TTFTP95, input.Result.VRAMMiB,
		input.Result.Config)
	resp, err := h.llm.ChatCompletion(ctx, []Message{
		{Role: "user", Content: prompt},
	}, nil)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}
