package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

// RunScan runs engine scan, model scan, and central sync in parallel, returning
// the aggregated result along with a time-ordered slice of progress events.
// HTTP handlers stream the events via SSE as they happen (they poll the
// returned slice after completion — MCP/CLI callers simply receive everything).
func RunScan(ctx context.Context, deps *Deps) (ScanResult, []Event, error) {
	var events []Event
	emit := func(t string, data map[string]any) {
		events = append(events, Event{Type: t, Timestamp: time.Now(), Data: data})
	}

	if deps == nil || deps.ToolDeps == nil {
		return ScanResult{}, nil, fmt.Errorf("onboarding scan: deps not initialized")
	}
	td := deps.ToolDeps

	type engineCollected struct {
		events  []Event
		engines []ScanEngineEntry
		err     error
	}
	type modelCollected struct {
		events []Event
		models []ScanModelEntry
		err    error
	}
	type centralCollected struct {
		events          []Event
		connected       bool
		configsPulled   int
		benchmarkPulled int
		err             error
	}

	engineCh := make(chan engineCollected, 1)
	modelCh := make(chan modelCollected, 1)
	centralCh := make(chan centralCollected, 1)

	now := func() time.Time { return time.Now() }

	// Engine scan goroutine
	go func() {
		var result engineCollected
		result.events = append(result.events, Event{Type: "scan_start", Timestamp: now(), Data: map[string]any{"phase": "engines"}})
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
			result.events = append(result.events, Event{
				Type:      "engine_found",
				Timestamp: now(),
				Data: map[string]any{
					"type":    entry.Type,
					"image":   entry.Image,
					"runtime": entry.RuntimeType,
				},
			})
		}
		result.events = append(result.events, Event{
			Type:      "scan_progress",
			Timestamp: now(),
			Data: map[string]any{
				"phase":  "engines",
				"status": "complete",
				"count":  len(result.engines),
			},
		})
		engineCh <- result
	}()

	// Model scan goroutine
	go func() {
		var result modelCollected
		result.events = append(result.events, Event{Type: "scan_start", Timestamp: now(), Data: map[string]any{"phase": "models"}})
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
			result.events = append(result.events, Event{
				Type:      "model_found",
				Timestamp: now(),
				Data: map[string]any{
					"name":       entry.Name,
					"format":     entry.Format,
					"size_bytes": entry.SizeBytes,
				},
			})
		}
		result.events = append(result.events, Event{
			Type:      "scan_progress",
			Timestamp: now(),
			Data: map[string]any{
				"phase":  "models",
				"status": "complete",
				"count":  len(result.models),
			},
		})
		modelCh <- result
	}()

	// Central sync goroutine (non-fatal — offline is OK)
	go func() {
		var result centralCollected
		result.events = append(result.events, Event{Type: "scan_start", Timestamp: now(), Data: map[string]any{"phase": "central_sync"}})
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
			result.events = append(result.events, Event{
				Type:      "central_synced",
				Timestamp: now(),
				Data: map[string]any{
					"connected": false,
					"error":     err.Error(),
				},
			})
			centralCh <- result
			return
		}
		result.connected = true
		result.configsPulled, result.benchmarkPulled = parseOnboardingCentralSyncCounts(raw)
		result.events = append(result.events, Event{
			Type:      "central_synced",
			Timestamp: now(),
			Data: map[string]any{
				"connected":         true,
				"configs_pulled":    result.configsPulled,
				"benchmarks_pulled": result.benchmarkPulled,
			},
		})
		centralCh <- result
	}()

	var result ScanResult

	// Collect in a loop (no mutex — single goroutine merges)
	for i := 0; i < 3; i++ {
		select {
		case <-ctx.Done():
			slog.Info("onboarding scan: client disconnected")
			return result, events, ctx.Err()
		case er := <-engineCh:
			events = append(events, er.events...)
			if er.err != nil {
				slog.Warn("onboarding scan: engine scan failed", "error", er.err)
			}
			result.Engines = er.engines
		case mr := <-modelCh:
			events = append(events, mr.events...)
			if mr.err != nil {
				slog.Warn("onboarding scan: model scan failed", "error", mr.err)
			}
			result.Models = mr.models
		case cr := <-centralCh:
			events = append(events, cr.events...)
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
