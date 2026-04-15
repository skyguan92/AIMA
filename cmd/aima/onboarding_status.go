package main

import (
	"context"
	"encoding/json"

	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// buildOnboardingStatusJSON is a thin delegate over onboarding.BuildStatus.
// Kept as a package-private function so the existing HTTP wiring in main.go /
// tooldeps_integration.go can keep calling the same name.
func buildOnboardingStatusJSON(ctx context.Context, ac *appContext, deps *mcp.ToolDeps) (json.RawMessage, error) {
	obDeps := buildOnboardingDepsStruct(ac, deps)
	result, err := onboarding.BuildStatus(ctx, obDeps)
	if err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
