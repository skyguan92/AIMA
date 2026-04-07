package central

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// LLMCompleter is the interface for LLM completions used by the Advisor.
type LLMCompleter interface {
	Complete(ctx context.Context, systemPrompt string, userPrompt string) (string, error)
}

// Advisor provides AI-powered recommendations using LLM + knowledge store context.
type Advisor struct {
	store CentralStore
	llm   LLMCompleter
}

// NewAdvisor creates an Advisor backed by the given store and LLM.
func NewAdvisor(store CentralStore, llm LLMCompleter) *Advisor {
	return &Advisor{store: store, llm: llm}
}

// RecommendRequest is the input for engine/config recommendation.
type RecommendRequest struct {
	Hardware string `json:"hardware"`
	Model    string `json:"model"`
	Goal     string `json:"goal,omitempty"` // e.g., "low-latency", "high-throughput"
}

// RecommendResponse is the structured LLM output for recommendations.
type RecommendResponse struct {
	Engine       string            `json:"engine"`
	Config       map[string]any    `json:"config"`
	Quantization string            `json:"quantization,omitempty"`
	Reasoning    string            `json:"reasoning"`
	Confidence   string            `json:"confidence"`
	Alternatives []json.RawMessage `json:"alternatives,omitempty"`
}

// Recommend queries the knowledge store for context and asks the LLM for a recommendation.
func (a *Advisor) Recommend(ctx context.Context, req RecommendRequest) (*RecommendResponse, *Advisory, error) {
	// Gather context from store
	configs, _ := a.store.QueryConfigurations(ctx, ConfigFilter{
		Hardware: req.Hardware,
		Model:    req.Model,
		Limit:    20,
	})
	benchmarks, _ := a.store.QueryBenchmarks(ctx, BenchmarkFilter{
		Hardware: req.Hardware,
		Model:    req.Model,
		Limit:    20,
	})

	userPrompt := a.buildRecommendPrompt(req, configs, benchmarks)

	raw, err := a.llm.Complete(ctx, promptRecommend, userPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("llm complete: %w", err)
	}

	var resp RecommendResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse llm response: %w", err)
	}

	// Store advisory
	adv := Advisory{
		ID:         genID("adv"),
		Type:       "recommendation",
		Severity:   "info",
		Hardware:   req.Hardware,
		Model:      req.Model,
		Engine:     resp.Engine,
		Title:      fmt.Sprintf("Recommendation: %s on %s", req.Model, req.Hardware),
		Summary:    resp.Reasoning,
		Details:    raw,
		Confidence: resp.Confidence,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	_ = a.store.InsertAdvisory(ctx, adv)

	return &resp, &adv, nil
}

// OptimizeRequest is the input for optimization suggestions.
type OptimizeRequest struct {
	ConfigID string `json:"config_id"`
	Hardware string `json:"hardware"`
	Model    string `json:"model"`
	Engine   string `json:"engine"`
}

// OptimizeResponse is the structured LLM output for optimizations.
type OptimizeResponse struct {
	Optimizations []json.RawMessage `json:"optimizations"`
	Reasoning     string            `json:"reasoning"`
	Confidence    string            `json:"confidence"`
}

// OptimizeScenario suggests optimizations for an existing deployment.
func (a *Advisor) OptimizeScenario(ctx context.Context, req OptimizeRequest) (*OptimizeResponse, *Advisory, error) {
	configs, _ := a.store.QueryConfigurations(ctx, ConfigFilter{
		Hardware: req.Hardware,
		Model:    req.Model,
		Engine:   req.Engine,
		Limit:    10,
	})
	benchmarks, _ := a.store.QueryBenchmarks(ctx, BenchmarkFilter{
		Hardware: req.Hardware,
		Model:    req.Model,
		Limit:    20,
	})

	userPrompt := a.buildOptimizePrompt(req, configs, benchmarks)

	raw, err := a.llm.Complete(ctx, promptOptimize, userPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("llm complete: %w", err)
	}

	var resp OptimizeResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse llm response: %w", err)
	}

	adv := Advisory{
		ID:         genID("adv"),
		Type:       "optimization",
		Severity:   "info",
		Hardware:   req.Hardware,
		Model:      req.Model,
		Engine:     req.Engine,
		Title:      fmt.Sprintf("Optimization: %s/%s on %s", req.Engine, req.Model, req.Hardware),
		Summary:    resp.Reasoning,
		Details:    raw,
		Confidence: resp.Confidence,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	_ = a.store.InsertAdvisory(ctx, adv)

	return &resp, &adv, nil
}

// GenerateScenarioRequest is the input for multi-model scenario generation.
type GenerateScenarioRequest struct {
	Hardware string   `json:"hardware"`
	Models   []string `json:"models"`
	Goal     string   `json:"goal,omitempty"`
}

// GenerateScenarioResponse is the structured LLM output for scenario generation.
type GenerateScenarioResponse struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Deployments []json.RawMessage `json:"deployments"`
	TotalVRAM   int               `json:"total_vram_mib"`
	Reasoning   string            `json:"reasoning"`
	Confidence  string            `json:"confidence"`
}

// GenerateScenario creates a multi-model deployment plan.
func (a *Advisor) GenerateScenario(ctx context.Context, req GenerateScenarioRequest) (*GenerateScenarioResponse, *Scenario, error) {
	// Gather context for each model
	var allConfigs []Configuration
	for _, model := range req.Models {
		configs, _ := a.store.QueryConfigurations(ctx, ConfigFilter{
			Hardware: req.Hardware,
			Model:    model,
			Limit:    5,
		})
		allConfigs = append(allConfigs, configs...)
	}

	userPrompt := a.buildScenarioPrompt(req, allConfigs)

	raw, err := a.llm.Complete(ctx, promptGenerateScenario, userPrompt)
	if err != nil {
		return nil, nil, fmt.Errorf("llm complete: %w", err)
	}

	var resp GenerateScenarioResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, nil, fmt.Errorf("parse llm response: %w", err)
	}

	modelsJSON, _ := json.Marshal(req.Models)
	scenario := Scenario{
		ID:          genID("scn"),
		Name:        resp.Name,
		Description: resp.Description,
		Hardware:    req.Hardware,
		Models:      string(modelsJSON),
		Config:      raw,
		Source:      "advisor",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	_ = a.store.InsertScenario(ctx, scenario)

	return &resp, &scenario, nil
}

// --- prompt builders ---

func (a *Advisor) buildRecommendPrompt(req RecommendRequest, configs []Configuration, benchmarks []BenchmarkResult) string {
	prompt := fmt.Sprintf("Hardware: %s\nModel: %s\n", req.Hardware, req.Model)
	if req.Goal != "" {
		prompt += fmt.Sprintf("Goal: %s\n", req.Goal)
	}
	if len(configs) > 0 {
		prompt += fmt.Sprintf("\nExisting configurations (%d):\n", len(configs))
		for _, c := range configs {
			prompt += fmt.Sprintf("- Engine: %s, Status: %s, Config: %s\n", c.EngineType, c.Status, c.Config)
		}
	}
	if len(benchmarks) > 0 {
		prompt += fmt.Sprintf("\nBenchmark results (%d):\n", len(benchmarks))
		for _, b := range benchmarks {
			prompt += fmt.Sprintf("- ConfigID: %s, Throughput: %.1f tps, TTFT_p50: %.1f ms, VRAM: %d MiB\n",
				b.ConfigID, b.ThroughputTPS, b.TTFTP50ms, b.VRAMUsageMiB)
		}
	}
	return prompt
}

func (a *Advisor) buildOptimizePrompt(req OptimizeRequest, configs []Configuration, benchmarks []BenchmarkResult) string {
	prompt := fmt.Sprintf("Hardware: %s\nModel: %s\nEngine: %s\n", req.Hardware, req.Model, req.Engine)
	if req.ConfigID != "" {
		prompt += fmt.Sprintf("Current config ID: %s\n", req.ConfigID)
	}
	if len(configs) > 0 {
		prompt += fmt.Sprintf("\nExisting configurations (%d):\n", len(configs))
		for _, c := range configs {
			prompt += fmt.Sprintf("- ID: %s, Status: %s, Config: %s\n", c.ID, c.Status, c.Config)
		}
	}
	if len(benchmarks) > 0 {
		prompt += fmt.Sprintf("\nBenchmark results (%d):\n", len(benchmarks))
		for _, b := range benchmarks {
			prompt += fmt.Sprintf("- ConfigID: %s, Throughput: %.1f tps, TTFT_p50: %.1f ms, VRAM: %d MiB, GPU_Util: %.0f%%\n",
				b.ConfigID, b.ThroughputTPS, b.TTFTP50ms, b.VRAMUsageMiB, b.GPUUtilPct)
		}
	}
	return prompt
}

func (a *Advisor) buildScenarioPrompt(req GenerateScenarioRequest, configs []Configuration) string {
	modelsStr := ""
	for i, m := range req.Models {
		if i > 0 {
			modelsStr += ", "
		}
		modelsStr += m
	}
	prompt := fmt.Sprintf("Hardware: %s\nModels to deploy: %s\n", req.Hardware, modelsStr)
	if req.Goal != "" {
		prompt += fmt.Sprintf("Goal: %s\n", req.Goal)
	}
	if len(configs) > 0 {
		prompt += fmt.Sprintf("\nKnown configurations for these models (%d):\n", len(configs))
		for _, c := range configs {
			prompt += fmt.Sprintf("- Model: %s, Engine: %s, Config: %s\n", c.Model, c.EngineType, c.Config)
		}
	}
	return prompt
}
