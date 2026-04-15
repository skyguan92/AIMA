package main

import (
	"context"
	"encoding/json"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// buildModelRecommendations is a thin delegate to onboarding.Recommend.
// Called by the HTTP handler in main.go (see onboarding-recommend endpoint).
// An empty locale falls back to "en" inside onboarding.Recommend.
func buildModelRecommendations(ctx context.Context, ac *appContext, deps *mcp.ToolDeps, locale string) (json.RawMessage, error) {
	obDeps := buildOnboardingDepsStruct(ac, deps)
	result, err := onboarding.Recommend(ctx, obDeps, locale)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
