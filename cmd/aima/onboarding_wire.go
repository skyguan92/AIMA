package main

import (
	"context"
	"log/slog"

	"github.com/jguan/aima/catalog"
	"github.com/jguan/aima/internal/knowledge"
	"github.com/jguan/aima/internal/mcp"
	"github.com/jguan/aima/internal/onboarding"
)

// buildOnboardingDepsStruct composes an *onboarding.Deps from the cmd/aima
// appContext and MCP ToolDeps. The `BuildHardwareInfo` and `DetectHWProfile`
// closures are how we expose cmd/aima's package-private helpers to the new
// internal/onboarding package (which cannot import cmd/aima).
func buildOnboardingDepsStruct(ac *appContext, deps *mcp.ToolDeps) *onboarding.Deps {
	obDeps := &onboarding.Deps{
		ToolDeps:       deps,
		FirstRunPolicy: loadOnboardingFirstRunPolicy(),
	}
	if ac != nil {
		obDeps.Cat = ac.cat
		obDeps.DB = ac.db
		obDeps.KStore = ac.kStore

		rtName := ""
		if ac.rt != nil {
			rtName = ac.rt.Name()
		}
		cat := ac.cat

		obDeps.BuildHardwareInfo = func(ctx context.Context) knowledge.HardwareInfo {
			return buildHardwareInfo(ctx, cat, rtName)
		}
		obDeps.DetectHWProfile = func(ctx context.Context) string {
			if cat == nil {
				return ""
			}
			return detectHWProfile(ctx, cat)
		}
	}
	return obDeps
}

func loadOnboardingFirstRunPolicy() *onboarding.FirstRunPolicy {
	raw, err := catalog.FS.ReadFile("onboarding-policy.yaml")
	if err != nil {
		return nil
	}
	policy, err := onboarding.ParseFirstRunPolicyYAML(raw)
	if err != nil {
		slog.Warn("failed to parse onboarding first-run policy", "error", err)
		return nil
	}
	return policy
}
