package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jguan/aima/internal/knowledge"
	"github.com/jguan/aima/internal/mcp"
	runtimePkg "github.com/jguan/aima/internal/runtime"
)

func TestComputeFitScore(t *testing.T) {
	tests := []struct {
		name           string
		hw             knowledge.HardwareInfo
		variant        *knowledge.ModelVariant
		fit            *knowledge.FitReport
		engineStatus   recommendedEngineStatus
		modelAvailable bool
		goldenExists   bool
		wantMin        int
		wantMax        int
	}{
		{
			name: "exact arch + golden + local engine = high score",
			hw: knowledge.HardwareInfo{
				GPUArch:    "Ada",
				GPUVRAMMiB: 24576,
				GPUCount:   1,
			},
			variant: &knowledge.ModelVariant{
				Hardware: knowledge.ModelVariantHardware{
					GPUArch:     "Ada",
					VRAMMinMiB:  16000,
					GPUCountMin: 1,
				},
			},
			fit: &knowledge.FitReport{
				Fit:         true,
				Adjustments: make(map[string]any),
			},
			engineStatus:   recommendedEngineStatus{Installed: true},
			modelAvailable: true,
			goldenExists:   true,
			wantMin:        80,
			wantMax:        100,
		},
		{
			name: "multi-GPU requirement lowers score",
			hw: knowledge.HardwareInfo{
				GPUArch:    "Ada",
				GPUVRAMMiB: 24576,
				GPUCount:   2,
			},
			variant: &knowledge.ModelVariant{
				Hardware: knowledge.ModelVariantHardware{
					GPUArch:     "Ada",
					VRAMMinMiB:  40000,
					GPUCountMin: 2,
				},
			},
			fit: &knowledge.FitReport{
				Fit:         true,
				Adjustments: make(map[string]any),
			},
			engineStatus:   recommendedEngineStatus{Installed: false},
			modelAvailable: false,
			goldenExists:   false,
			wantMin:        50,
			wantMax:        70,
		},
		{
			name: "no performance data still scores non-zero",
			hw: knowledge.HardwareInfo{
				GPUArch:    "CUDA",
				GPUVRAMMiB: 8192,
				GPUCount:   1,
			},
			variant: &knowledge.ModelVariant{
				Hardware: knowledge.ModelVariantHardware{
					GPUArch:     "CUDA",
					VRAMMinMiB:  4000,
					GPUCountMin: 1,
				},
			},
			fit: &knowledge.FitReport{
				Fit:         true,
				Adjustments: make(map[string]any),
			},
			engineStatus:   recommendedEngineStatus{Installed: false},
			modelAvailable: false,
			goldenExists:   false,
			wantMin:        50,
			wantMax:        80,
		},
		{
			name: "fit=false returns zero",
			hw: knowledge.HardwareInfo{
				GPUArch:    "Ada",
				GPUVRAMMiB: 4096,
				GPUCount:   1,
			},
			variant: &knowledge.ModelVariant{
				Hardware: knowledge.ModelVariantHardware{
					GPUArch:     "Ada",
					VRAMMinMiB:  24000,
					GPUCountMin: 1,
				},
			},
			fit: &knowledge.FitReport{
				Fit:         false,
				Reason:      "insufficient VRAM",
				Adjustments: make(map[string]any),
			},
			engineStatus:   recommendedEngineStatus{Installed: false},
			modelAvailable: false,
			goldenExists:   false,
			wantMin:        0,
			wantMax:        0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := computeFitScore(tt.hw, tt.variant, tt.fit, tt.engineStatus, tt.modelAvailable, tt.goldenExists)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("computeFitScore() = %d, want [%d, %d]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestBuildModelRecommendations_EmptyCatalog(t *testing.T) {
	cat := &knowledge.Catalog{
		ModelAssets: []knowledge.ModelAsset{}, // empty
	}

	deps := &mcp.ToolDeps{}
	ac := &appContext{
		cat: cat,
		rt:  &stubRuntime{},
	}

	data, err := buildModelRecommendations(context.Background(), ac, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		Recommendations []modelRecommendation `json:"recommendations"`
		TotalModels     int                   `json:"total_models_evaluated"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.TotalModels != 0 {
		t.Errorf("total_models_evaluated = %d, want 0", result.TotalModels)
	}
	if len(result.Recommendations) != 0 {
		t.Errorf("recommendations length = %d, want 0", len(result.Recommendations))
	}
}

// stubRuntime implements runtime.Runtime with no-op methods for testing.
type stubRuntime struct{}

func (s *stubRuntime) Name() string                                                    { return "docker" }
func (s *stubRuntime) Deploy(_ context.Context, _ *runtimePkg.DeployRequest) error     { return nil }
func (s *stubRuntime) Delete(_ context.Context, _ string) error                        { return nil }
func (s *stubRuntime) Status(_ context.Context, _ string) (*runtimePkg.DeploymentStatus, error) { return nil, nil }
func (s *stubRuntime) List(_ context.Context) ([]*runtimePkg.DeploymentStatus, error)  { return nil, nil }
func (s *stubRuntime) Logs(_ context.Context, _ string, _ int) (string, error)         { return "", nil }
