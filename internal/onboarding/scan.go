package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const onboardingCentralSyncTimeout = 2 * time.Second

// parseOnboardingCentralSyncCounts extracts configuration/benchmark import
// counters from a central sync response. Supports both the modern nested
// `knowledge_import.imported.*` shape and the legacy flat one.
func parseOnboardingCentralSyncCounts(raw json.RawMessage) (int, int) {
	var nested struct {
		KnowledgeImport struct {
			Imported struct {
				Configurations   int `json:"configurations"`
				BenchmarkResults int `json:"benchmark_results"`
			} `json:"imported"`
		} `json:"knowledge_import"`
	}
	if err := json.Unmarshal(raw, &nested); err == nil {
		if nested.KnowledgeImport.Imported.Configurations != 0 || nested.KnowledgeImport.Imported.BenchmarkResults != 0 {
			return nested.KnowledgeImport.Imported.Configurations, nested.KnowledgeImport.Imported.BenchmarkResults
		}
	}

	var legacy struct {
		Configurations int `json:"configurations_imported"`
		Benchmarks     int `json:"benchmarks_imported"`
	}
	if err := json.Unmarshal(raw, &legacy); err == nil {
		return legacy.Configurations, legacy.Benchmarks
	}
	return 0, 0
}

// RunScan runs engine scan, model scan, and central sync in parallel. Each
// event is delivered to `sink` (if non-nil) the moment it is produced, so SSE
// handlers can stream progress in real time. The full ordered slice of events
// is also returned for MCP/CLI callers that want a single response.
func RunScan(ctx context.Context, deps *Deps, sink EventSink) (ScanResult, []Event, error) {
	if deps == nil || deps.ToolDeps == nil {
		return ScanResult{}, nil, fmt.Errorf("onboarding scan: deps not initialized")
	}
	td := deps.ToolDeps

	var (
		mu     sync.Mutex
		events []Event
	)
	emit := func(t string, data map[string]any) {
		ev := Event{Type: t, Timestamp: time.Now(), Data: data}
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
		if sink != nil {
			sink(ev)
		}
	}

	type engineResult struct {
		engines []ScanEngineEntry
		err     error
	}
	type modelResult struct {
		models []ScanModelEntry
		err    error
	}
	type centralResult struct {
		connected       bool
		configsPulled   int
		benchmarkPulled int
		err             error
	}

	engineCh := make(chan engineResult, 1)
	modelCh := make(chan modelResult, 1)
	centralCh := make(chan centralResult, 1)

	// Engine scan goroutine
	go func() {
		emit("scan_start", map[string]any{"phase": "engines"})
		var result engineResult
		if td.ScanEngines == nil {
			result.err = fmt.Errorf("engine scan not available")
			engineCh <- result
			return
		}
		raw, err := td.ScanEngines(ctx, "auto", false)
		if err != nil {
			result.err = err
			engineCh <- result
			return
		}
		var engines []struct {
			Type        string `json:"type"`
			Image       string `json:"image"`
			RuntimeType string `json:"runtime_type"`
		}
		if err := json.Unmarshal(raw, &engines); err != nil {
			result.err = fmt.Errorf("parse engine scan result: %w", err)
			engineCh <- result
			return
		}
		for _, e := range engines {
			entry := ScanEngineEntry{
				Type:        e.Type,
				Image:       e.Image,
				RuntimeType: e.RuntimeType,
			}
			result.engines = append(result.engines, entry)
			emit("engine_found", map[string]any{
				"type":    entry.Type,
				"image":   entry.Image,
				"runtime": entry.RuntimeType,
			})
		}
		emit("scan_progress", map[string]any{
			"phase":  "engines",
			"status": "complete",
			"count":  len(result.engines),
		})
		engineCh <- result
	}()

	// Model scan goroutine
	go func() {
		emit("scan_start", map[string]any{"phase": "models"})
		var result modelResult
		if td.ScanModels == nil {
			result.err = fmt.Errorf("model scan not available")
			modelCh <- result
			return
		}
		raw, err := td.ScanModels(ctx)
		if err != nil {
			result.err = err
			modelCh <- result
			return
		}
		var models []struct {
			Name      string `json:"name"`
			Format    string `json:"format"`
			SizeBytes int64  `json:"size_bytes"`
		}
		if err := json.Unmarshal(raw, &models); err != nil {
			result.err = fmt.Errorf("parse model scan result: %w", err)
			modelCh <- result
			return
		}
		for _, m := range models {
			entry := ScanModelEntry{
				Name:      m.Name,
				Format:    m.Format,
				SizeBytes: m.SizeBytes,
			}
			result.models = append(result.models, entry)
			emit("model_found", map[string]any{
				"name":       entry.Name,
				"format":     entry.Format,
				"size_bytes": entry.SizeBytes,
			})
		}
		emit("scan_progress", map[string]any{
			"phase":  "models",
			"status": "complete",
			"count":  len(result.models),
		})
		modelCh <- result
	}()

	// Central sync goroutine (non-fatal — offline is OK)
	go func() {
		emit("scan_start", map[string]any{"phase": "central_sync"})
		var result centralResult
		if td.SyncPull == nil {
			result.err = fmt.Errorf("central sync not available")
			centralCh <- result
			return
		}
		syncCtx, cancel := context.WithTimeout(ctx, onboardingCentralSyncTimeout)
		defer cancel()
		raw, err := td.SyncPull(syncCtx)
		if err != nil {
			result.err = err
			emit("central_synced", map[string]any{
				"connected": false,
				"error":     err.Error(),
			})
			centralCh <- result
			return
		}
		result.connected = true
		result.configsPulled, result.benchmarkPulled = parseOnboardingCentralSyncCounts(raw)
		emit("central_synced", map[string]any{
			"connected":         true,
			"configs_pulled":    result.configsPulled,
			"benchmarks_pulled": result.benchmarkPulled,
		})
		centralCh <- result
	}()

	var result ScanResult

	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			slog.Info("onboarding scan: client disconnected")
			return result, events, ctx.Err()
		case er := <-engineCh:
			if er.err != nil {
				slog.Warn("onboarding scan: engine scan failed", "error", er.err)
			}
			result.Engines = er.engines
		case mr := <-modelCh:
			if mr.err != nil {
				slog.Warn("onboarding scan: model scan failed", "error", mr.err)
			}
			result.Models = mr.models
		case cr := <-centralCh:
			if cr.err != nil {
				slog.Debug("onboarding scan: central sync failed (offline is OK)", "error", cr.err)
			}
			result.CentralConnected = cr.connected
			result.ConfigsPulled = cr.configsPulled
			result.BenchmarksPulled = cr.benchmarkPulled
		}
	}

	if result.Engines == nil {
		result.Engines = []ScanEngineEntry{}
	}
	if result.Models == nil {
		result.Models = []ScanModelEntry{}
	}

	emit("scan_complete", map[string]any{
		"engines":           len(result.Engines),
		"models":            len(result.Models),
		"central_connected": result.CentralConnected,
	})

	return result, events, nil
}
