package onboarding

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jguan/aima/internal/mcp"
)

// TestRunInit_ImportsEnginesAfterReadyK3SInit ports the behavioral assertion
// previously held by cmd/aima/onboarding_deploy_test.go:
// TestBuildOnboardingDeps_ImportsEnginesAfterReadyK3SInit. The decorator that
// bolted engine-import onto StackInit has been retired; the same side effect
// is now inlined in onboarding.RunInit. This test pins that behavior.
func TestRunInit_ImportsEnginesAfterReadyK3SInit(t *testing.T) {
	var scanCalls int
	var gotRuntimeFilter string
	var gotAutoImport bool

	td := &mcp.ToolDeps{
		// Force needs_init=true so RunInit progresses past the early exit.
		StackStatus: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"components":[{"name":"docker","ready":false},{"name":"k3s","ready":false}],"all_ready":false}`), nil
		},
		StackPreflight: func(ctx context.Context, tier string) (json.RawMessage, error) {
			return json.RawMessage(`[]`), nil
		},
		StackInit: func(ctx context.Context, tier string, allowDownload bool) (json.RawMessage, error) {
			return json.RawMessage(`{"components":[{"name":"docker","ready":true,"message":"ready"}],"all_ready":true}`), nil
		},
		ScanEngines: func(ctx context.Context, runtimeFilter string, autoImport bool) (json.RawMessage, error) {
			scanCalls++
			gotRuntimeFilter = runtimeFilter
			gotAutoImport = autoImport
			return json.RawMessage(`[]`), nil
		},
	}

	// Allow RunInit to pass the CanAutoInit guard without requiring root.
	origDetect := DetectOnboardingInitCapability
	DetectOnboardingInitCapability = func(deps *mcp.ToolDeps) (bool, string) {
		return true, ""
	}
	defer func() { DetectOnboardingInitCapability = origDetect }()

	deps := &Deps{ToolDeps: td}

	result, _, err := RunInit(context.Background(), deps, "k3s", true)
	if err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	if !result.AllReady {
		t.Fatalf("RunInit AllReady = false, want true")
	}
	if scanCalls != 1 {
		t.Fatalf("ScanEngines call count = %d, want 1", scanCalls)
	}
	if gotRuntimeFilter != "auto" || !gotAutoImport {
		t.Fatalf("ScanEngines(%q, %v), want autoImport on auto runtime", gotRuntimeFilter, gotAutoImport)
	}
}

// TestRunInit_SkipsEngineImportOnDockerTier guards against regressing the
// tier gate: the post-init engine scan is k3s-only. Docker installs must not
// trigger it.
func TestRunInit_SkipsEngineImportOnDockerTier(t *testing.T) {
	var scanCalls int

	td := &mcp.ToolDeps{
		StackStatus: func(ctx context.Context) (json.RawMessage, error) {
			return json.RawMessage(`{"components":[{"name":"docker","ready":false},{"name":"k3s","ready":false}],"all_ready":false}`), nil
		},
		StackPreflight: func(ctx context.Context, tier string) (json.RawMessage, error) {
			return json.RawMessage(`[]`), nil
		},
		StackInit: func(ctx context.Context, tier string, allowDownload bool) (json.RawMessage, error) {
			return json.RawMessage(`{"components":[{"name":"docker","ready":true,"message":"ready"}],"all_ready":true}`), nil
		},
		ScanEngines: func(ctx context.Context, runtimeFilter string, autoImport bool) (json.RawMessage, error) {
			scanCalls++
			return json.RawMessage(`[]`), nil
		},
	}

	origDetect := DetectOnboardingInitCapability
	DetectOnboardingInitCapability = func(deps *mcp.ToolDeps) (bool, string) {
		return true, ""
	}
	defer func() { DetectOnboardingInitCapability = origDetect }()

	deps := &Deps{ToolDeps: td}

	if _, _, err := RunInit(context.Background(), deps, "docker", true); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	if scanCalls != 0 {
		t.Fatalf("ScanEngines call count = %d, want 0 on docker tier", scanCalls)
	}
}
