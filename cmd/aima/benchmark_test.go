package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	state "github.com/jguan/aima/internal"
	benchpkg "github.com/jguan/aima/internal/benchmark"
	"github.com/jguan/aima/internal/runtime"
)

func TestSaveBenchmarkResultPersistsDeployConfig(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	origCollect := collectBenchmarkSystemMetrics
	collectBenchmarkSystemMetrics = func(context.Context) benchmarkSystemMetrics {
		return benchmarkSystemMetrics{
			VRAMUsageMiB:      32768,
			RAMUsageMiB:       8192,
			PowerDrawWatts:    412.5,
			GPUUtilizationPct: 92,
			CPUUsagePct:       63.5,
		}
	}
	t.Cleanup(func() { collectBenchmarkSystemMetrics = origCollect })

	benchmarkID, configID, saved, err := saveBenchmarkResult(ctx, db,
		"nvidia-rtx4090-x86", "sglang-kt", "qwen3-4b",
		&benchpkg.RunResult{
			ThroughputTPS:   42.5,
			TTFTP95ms:       123.4,
			TTFTP50ms:       90,
			AvgInputTokens:  2048,
			AvgOutputTokens: 256,
			TotalRequests:   8,
		},
		map[string]any{
			"mem_fraction_static":  0.9,
			"max_running_requests": 16,
			"trust_remote_code":    true,
		},
		benchmarkSystemMetrics{},
		2, 2048, 256, "explorer validate")
	if err != nil {
		t.Fatalf("saveBenchmarkResult: %v", err)
	}
	if benchmarkID == "" || configID == "" {
		t.Fatalf("ids = (%q, %q), want non-empty", benchmarkID, configID)
	}
	if saved == nil {
		t.Fatal("expected saved benchmark row")
	}
	if saved.CPUUsagePct != 63.5 {
		t.Fatalf("saved CPUUsagePct = %v, want 63.5", saved.CPUUsagePct)
	}

	cfg, err := db.GetConfiguration(ctx, configID)
	if err != nil {
		t.Fatalf("GetConfiguration: %v", err)
	}
	var gotConfig map[string]any
	if err := json.Unmarshal([]byte(cfg.Config), &gotConfig); err != nil {
		t.Fatalf("Unmarshal config: %v", err)
	}
	wantConfig := map[string]any{
		"mem_fraction_static":  float64(0.9),
		"max_running_requests": float64(16),
		"trust_remote_code":    true,
	}
	if !reflect.DeepEqual(gotConfig, wantConfig) {
		t.Fatalf("Config = %#v, want %#v", gotConfig, wantConfig)
	}

	results, err := db.ListBenchmarkResults(ctx, []string{configID}, 10)
	if err != nil {
		t.Fatalf("ListBenchmarkResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("benchmark results = %d, want 1", len(results))
	}
	if results[0].ThroughputTPS != 42.5 {
		t.Fatalf("ThroughputTPS = %v, want 42.5", results[0].ThroughputTPS)
	}
	if results[0].VRAMUsageMiB != 32768 {
		t.Fatalf("VRAMUsageMiB = %d, want 32768", results[0].VRAMUsageMiB)
	}
	if results[0].RAMUsageMiB != 8192 {
		t.Fatalf("RAMUsageMiB = %d, want 8192", results[0].RAMUsageMiB)
	}
	if results[0].PowerDrawWatts != 412.5 {
		t.Fatalf("PowerDrawWatts = %v, want 412.5", results[0].PowerDrawWatts)
	}
	if results[0].GPUUtilPct != 92 {
		t.Fatalf("GPUUtilPct = %v, want 92", results[0].GPUUtilPct)
	}
	if results[0].CPUUsagePct != 63.5 {
		t.Fatalf("CPUUsagePct = %v, want 63.5", results[0].CPUUsagePct)
	}
}

func TestLookupEngineAssetMetadataResolvesTypeToHardwareCompatibleAsset(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sqlDB := db.RawDB()
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO hardware_profiles (id, name, gpu_arch) VALUES ('nvidia-rtx4090-x86', 'RTX 4090', 'Ada')`); err != nil {
		t.Fatalf("insert hardware profile: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO engine_assets (id, type, version, image_name, image_tag)
		 VALUES ('sglang-kt-ada', 'sglang-kt', '0.5.9-kt0.5.2', 'aima/sglang-kt', 'v0.5.9-kt0.5.2')`); err != nil {
		t.Fatalf("insert engine asset: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx,
		`INSERT INTO engine_hardware_compat (engine_id, hardware_id) VALUES ('sglang-kt-ada', 'nvidia-rtx4090-x86')`); err != nil {
		t.Fatalf("insert engine compat: %v", err)
	}

	version, image, err := db.LookupEngineAssetMetadata(ctx, "sglang-kt", "nvidia-rtx4090-x86")
	if err != nil {
		t.Fatalf("LookupEngineAssetMetadata: %v", err)
	}
	if version != "0.5.9-kt0.5.2" {
		t.Fatalf("version = %q, want 0.5.9-kt0.5.2", version)
	}
	if image != "aima/sglang-kt:v0.5.9-kt0.5.2" {
		t.Fatalf("image = %q, want aima/sglang-kt:v0.5.9-kt0.5.2", image)
	}
}

func TestSelectReadyDeployConfigSkipsNonReadyMatches(t *testing.T) {
	readyConfig := map[string]any{"mem_fraction_static": 0.85}
	got := selectReadyDeployConfig("sglang-kt", nil, []matchedDeployment{
		{
			Status: &runtime.DeploymentStatus{
				Engine: "sglang-kt",
				Ready:  false,
				Config: map[string]any{"mem_fraction_static": 0.27},
			},
		},
		{
			Status: &runtime.DeploymentStatus{
				Labels: map[string]string{"aima.dev/engine": "sglang-kt"},
				Ready:  true,
				Config: readyConfig,
			},
		},
	})
	if !reflect.DeepEqual(got, readyConfig) {
		t.Fatalf("selectReadyDeployConfig = %#v, want %#v", got, readyConfig)
	}

	got = selectReadyDeployConfig("sglang-kt", nil, []matchedDeployment{{
		Status: &runtime.DeploymentStatus{
			Engine: "sglang-kt",
			Ready:  false,
			Config: map[string]any{"mem_fraction_static": 0.27},
		},
	}})
	if got != nil {
		t.Fatalf("selectReadyDeployConfig with only non-ready matches = %#v, want nil", got)
	}
}

func TestBenchmarkMetricsWindowSnapshotUsesPeakAndAverage(t *testing.T) {
	window := &benchmarkMetricsWindow{}
	window.observe(benchmarkSystemMetrics{
		VRAMUsageMiB:      2048,
		RAMUsageMiB:       8192,
		CPUUsagePct:       40,
		GPUUtilizationPct: 20,
		PowerDrawWatts:    100,
	})
	window.observe(benchmarkSystemMetrics{
		VRAMUsageMiB:      4096,
		RAMUsageMiB:       4096,
		CPUUsagePct:       60,
		GPUUtilizationPct: 40,
		PowerDrawWatts:    200,
	})

	got := window.snapshot()
	if got.VRAMUsageMiB != 4096 {
		t.Fatalf("VRAMUsageMiB = %d, want 4096", got.VRAMUsageMiB)
	}
	if got.RAMUsageMiB != 8192 {
		t.Fatalf("RAMUsageMiB = %d, want 8192", got.RAMUsageMiB)
	}
	if got.CPUUsagePct != 50 {
		t.Fatalf("CPUUsagePct = %v, want 50", got.CPUUsagePct)
	}
	if got.GPUUtilizationPct != 30 {
		t.Fatalf("GPUUtilizationPct = %v, want 30", got.GPUUtilizationPct)
	}
	if got.PowerDrawWatts != 150 {
		t.Fatalf("PowerDrawWatts = %v, want 150", got.PowerDrawWatts)
	}
}
