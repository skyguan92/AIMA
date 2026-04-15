package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/jguan/aima/internal/engine"
	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
	"github.com/jguan/aima/internal/stack"
)

// buildOnboardingDeps wires the onboarding-related tool dependencies.
// Step 2 of the refactor will retire this decorator entirely; Step 1 keeps it
// in place so that the MCP tool surface (onboarding_completed side effect,
// post-k3s-init engine import) remains behaviorally identical while the
// business logic moves to internal/onboarding.
func buildOnboardingDeps(ac *appContext, deps *mcp.ToolDeps) {
	deps.RecommendModels = func(ctx context.Context) (json.RawMessage, error) {
		return buildModelRecommendations(ctx, ac, deps)
	}
	if deps.StackInit != nil {
		baseStackInit := deps.StackInit
		deps.StackInit = func(ctx context.Context, tier string, allowDownload bool) (json.RawMessage, error) {
			data, err := baseStackInit(ctx, tier, allowDownload)
			if err != nil {
				return data, err
			}
			result, decodeErr := decodeStackInitResult(data)
			if decodeErr != nil || !result.AllReady || normalizeOnboardingInitTier(tier) != "k3s" || deps.ScanEngines == nil {
				return data, nil
			}

			resp := stackInitResponse{
				InitResult:            result,
				EngineImportTriggered: true,
			}
			importCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			if _, scanErr := deps.ScanEngines(importCtx, "auto", true); scanErr != nil {
				resp.EngineImportWarning = scanErr.Error()
				slog.Warn("stack init: post-init engine import failed", "tier", tier, "error", scanErr)
			}
			encoded, marshalErr := json.Marshal(resp)
			if marshalErr != nil {
				return data, nil
			}
			return encoded, nil
		}
	}
	if deps.DeployRun != nil {
		baseDeployRun := deps.DeployRun
		deps.DeployRun = func(ctx context.Context, model, engineType, slot string, configOverrides map[string]any, noPull bool,
			onPhase func(string, string), onEngineProgress func(engine.ProgressEvent),
		) (json.RawMessage, error) {
			data, err := baseDeployRun(ctx, model, engineType, slot, configOverrides, noPull, onPhase, onEngineProgress)
			if err != nil {
				return data, err
			}
			if !onboardingDeployCompleted(data) || deps.SetConfig == nil {
				return data, nil
			}
			persistCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if cfgErr := deps.SetConfig(persistCtx, "onboarding_completed", "true"); cfgErr != nil {
				slog.Warn("onboarding deploy: failed to mark onboarding completed", "error", cfgErr)
			}
			return data, nil
		}
	}
}

type stackInitResponse struct {
	stack.InitResult
	EngineImportTriggered bool   `json:"engine_import_triggered,omitempty"`
	EngineImportWarning   string `json:"engine_import_warning,omitempty"`
}

func decodeStackInitResult(data json.RawMessage) (stack.InitResult, error) {
	var result stack.InitResult
	if err := json.Unmarshal(data, &result); err != nil {
		return stack.InitResult{}, err
	}
	return result, nil
}

func onboardingDeployCompleted(data json.RawMessage) bool {
	result, err := decodeOnboardingDeployResult(data)
	if err != nil {
		return false
	}
	return result.ready()
}

// buildModelRecommendations is a thin delegate to onboarding.Recommend.
// Kept under the legacy name so existing callers (buildOnboardingDeps,
// tests) don't need to change.
func buildModelRecommendations(ctx context.Context, ac *appContext, deps *mcp.ToolDeps) (json.RawMessage, error) {
	obDeps := buildOnboardingDepsStruct(ac, deps)
	result, err := onboarding.Recommend(ctx, obDeps, "en")
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
