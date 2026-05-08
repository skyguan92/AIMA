package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jguan/aima/internal/agent"
	"github.com/jguan/aima/internal/knowledge"
	"github.com/jguan/aima/internal/mcp"

	state "github.com/jguan/aima/internal"
)

func mustOpenTooldepsDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:): %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func boolPtr(value bool) *bool {
	return &value
}

func TestScanModelsPublishesModelDiscoveredOnlyForNewModels(t *testing.T) {
	ctx := context.Background()
	db := mustOpenTooldepsDB(t)

	root := t.TempDir()
	if err := writeScanModelFixture(filepath.Join(root, "new-model"), 11*1024*1024); err != nil {
		t.Fatalf("writeScanModelFixture: %v", err)
	}

	t.Setenv("AIMA_MODEL_DIR", root)
	t.Setenv("HOME", t.TempDir())

	bus := agent.NewEventBus()
	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	deps := &mcp.ToolDeps{}
	buildModelDeps(&appContext{
		cat:      &knowledge.Catalog{},
		db:       db,
		dataDir:  t.TempDir(),
		eventBus: bus,
	}, deps, func(context.Context, string, func(string, string), func(int64, int64)) error {
		return nil
	}, NewDownloadTracker(filepath.Join(t.TempDir(), "downloads")))

	data, err := deps.ScanModels(ctx)
	if err != nil {
		t.Fatalf("ScanModels: %v", err)
	}
	var models []map[string]any
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("Unmarshal scan data: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected at least one scanned model")
	}

	waitForDiscoveredModelEvent(t, sub, "new-model")
	drainExplorerEvents(sub)

	if _, err := deps.ScanModels(ctx); err != nil {
		t.Fatalf("second ScanModels: %v", err)
	}
	assertNoDiscoveredModelEvent(t, sub, "new-model")
}

func TestImportModelPublishesModelDiscovered(t *testing.T) {
	ctx := context.Background()
	db := mustOpenTooldepsDB(t)

	srcRoot := t.TempDir()
	dataDir := t.TempDir()
	modelDir := filepath.Join(srcRoot, "import-me")
	if err := writeScanModelFixture(modelDir, 512); err != nil {
		t.Fatalf("writeScanModelFixture: %v", err)
	}

	bus := agent.NewEventBus()
	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	deps := &mcp.ToolDeps{}
	buildModelDeps(&appContext{
		cat:      &knowledge.Catalog{},
		db:       db,
		dataDir:  dataDir,
		eventBus: bus,
	}, deps, func(context.Context, string, func(string, string), func(int64, int64)) error {
		return nil
	}, NewDownloadTracker(filepath.Join(t.TempDir(), "downloads")))

	data, err := deps.ImportModel(ctx, modelDir)
	if err != nil {
		t.Fatalf("ImportModel: %v", err)
	}
	var imported map[string]any
	if err := json.Unmarshal(data, &imported); err != nil {
		t.Fatalf("Unmarshal import data: %v", err)
	}
	if imported["name"] != "import-me" {
		t.Fatalf("imported name = %v, want import-me", imported["name"])
	}

	waitForDiscoveredModelEvent(t, sub, "import-me")
}

func TestRegisterCatalogLocalModelsRegistersLocalPathAssets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := mustOpenTooldepsDB(t)
	modelDir := filepath.Join(t.TempDir(), "funasr")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "configuration.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cat := &knowledge.Catalog{
		ModelAssets: []knowledge.ModelAsset{
			{
				Metadata: knowledge.ModelMetadata{
					Name:       "funasr-paraformer-onnx",
					Type:       "asr",
					Family:     "funasr",
					ModelClass: "pipeline",
				},
				UI: knowledge.ModelUI{
					Role:          "deployable",
					DisplayNote:   "Speech recognition pipeline",
					DisplayNoteZh: "语音识别流水线",
				},
				Capabilities: knowledge.ModelCapabilities{StandaloneDeploy: boolPtr(true)},
				Storage: knowledge.ModelStorage{
					Formats: []string{"onnx"},
					Sources: []knowledge.ModelSource{
						{Type: "local_path", Path: modelDir},
					},
				},
				Variants: []knowledge.ModelVariant{
					{
						Name:   "funasr-paraformer-onnx-cpu-arm64",
						Format: "onnx",
					},
				},
			},
		},
	}

	if err := registerCatalogLocalModels(ctx, cat, db); err != nil {
		t.Fatalf("registerCatalogLocalModels: %v", err)
	}

	model, err := db.GetModel(ctx, "funasr-paraformer-onnx")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if model.Path != modelDir {
		t.Fatalf("Path = %q, want %q", model.Path, modelDir)
	}
	if model.Type != "asr" {
		t.Fatalf("Type = %q, want asr", model.Type)
	}
	if model.Format != "onnx" {
		t.Fatalf("Format = %q, want onnx", model.Format)
	}
	if model.ModelClass != "pipeline" {
		t.Fatalf("ModelClass = %q, want pipeline", model.ModelClass)
	}
	if model.UIRole != "deployable" {
		t.Fatalf("UIRole = %q, want deployable", model.UIRole)
	}
	if model.StandaloneDeploy == nil || !*model.StandaloneDeploy {
		t.Fatalf("StandaloneDeploy = %v, want true", model.StandaloneDeploy)
	}
}

func TestListModelsDoesNotRegisterCatalogLocalModels(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := mustOpenTooldepsDB(t)
	modelDir := filepath.Join(t.TempDir(), "funasr")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	deps := &mcp.ToolDeps{}
	buildModelDeps(&appContext{
		cat: &knowledge.Catalog{
			ModelAssets: []knowledge.ModelAsset{{
				Metadata: knowledge.ModelMetadata{Name: "funasr-paraformer-onnx", Type: "asr", Family: "funasr"},
				Storage: knowledge.ModelStorage{
					Formats: []string{"onnx"},
					Sources: []knowledge.ModelSource{{Type: "local_path", Path: modelDir}},
				},
			}},
		},
		db:      db,
		dataDir: t.TempDir(),
	}, deps, func(context.Context, string, func(string, string), func(int64, int64)) error {
		return nil
	}, NewDownloadTracker(filepath.Join(t.TempDir(), "downloads")))

	if _, err := deps.ListModels(ctx); err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if _, err := db.GetModel(ctx, "funasr-paraformer-onnx"); err == nil {
		t.Fatal("expected ListModels to remain read-only and not register catalog local models")
	}
}

func TestListModelsAnnotatesCatalogDisplayFields(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := mustOpenTooldepsDB(t)
	if err := db.UpsertScannedModel(ctx, &state.Model{
		ID:     "catalog-model",
		Name:   "catalog-model",
		Type:   "asr",
		Path:   filepath.Join(t.TempDir(), "catalog-model"),
		Format: "onnx",
		Status: "registered",
	}); err != nil {
		t.Fatalf("UpsertScannedModel: %v", err)
	}

	deps := &mcp.ToolDeps{}
	buildModelDeps(&appContext{
		cat: &knowledge.Catalog{
			ModelAssets: []knowledge.ModelAsset{{
				Metadata: knowledge.ModelMetadata{
					Name:       "catalog-model",
					Type:       "asr",
					Family:     "funasr",
					ModelClass: "pipeline",
				},
				UI: knowledge.ModelUI{
					Role:          "component",
					DisplayNote:   "Catalog note",
					DisplayNoteZh: "目录说明",
				},
				Capabilities: knowledge.ModelCapabilities{StandaloneDeploy: boolPtr(false)},
			}},
		},
		db:      db,
		dataDir: t.TempDir(),
	}, deps, func(context.Context, string, func(string, string), func(int64, int64)) error {
		return nil
	}, NewDownloadTracker(filepath.Join(t.TempDir(), "downloads")))

	data, err := deps.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	var models []state.Model
	if err := json.Unmarshal(data, &models); err != nil {
		t.Fatalf("Unmarshal ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("model count = %d, want 1", len(models))
	}
	model := models[0]
	if model.UIRole != "component" {
		t.Fatalf("UIRole = %q, want component", model.UIRole)
	}
	if model.UIDisplayNoteZh != "目录说明" {
		t.Fatalf("UIDisplayNoteZh = %q, want 目录说明", model.UIDisplayNoteZh)
	}
	if model.StandaloneDeploy == nil || *model.StandaloneDeploy {
		t.Fatalf("StandaloneDeploy = %v, want false", model.StandaloneDeploy)
	}
}

func TestRegisterCatalogLocalModelsDoesNotDeleteSameNameDifferentPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := mustOpenTooldepsDB(t)

	userDir := filepath.Join(t.TempDir(), "user-copy")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("MkdirAll userDir: %v", err)
	}
	if err := db.UpsertScannedModel(ctx, &state.Model{
		ID:         "user-funasr",
		Name:       "funasr-paraformer-onnx",
		Type:       "asr",
		Path:       userDir,
		Format:     "onnx",
		ModelClass: "pipeline",
		Status:     "registered",
	}); err != nil {
		t.Fatalf("UpsertScannedModel(user): %v", err)
	}

	catalogDir := filepath.Join(t.TempDir(), "catalog-copy")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("MkdirAll catalogDir: %v", err)
	}

	cat := &knowledge.Catalog{
		ModelAssets: []knowledge.ModelAsset{{
			Metadata: knowledge.ModelMetadata{
				Name:       "funasr-paraformer-onnx",
				Type:       "asr",
				Family:     "funasr",
				ModelClass: "pipeline",
			},
			Storage: knowledge.ModelStorage{
				Formats: []string{"onnx"},
				Sources: []knowledge.ModelSource{
					{Type: "local_path", Path: catalogDir},
				},
			},
		}},
	}

	if err := registerCatalogLocalModels(ctx, cat, db); err != nil {
		t.Fatalf("registerCatalogLocalModels: %v", err)
	}

	models, err := db.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("model count = %d, want 2", len(models))
	}

	foundUser := false
	foundCatalog := false
	for _, model := range models {
		switch model.Path {
		case userDir:
			foundUser = true
		case catalogDir:
			foundCatalog = true
		}
	}
	if !foundUser || !foundCatalog {
		t.Fatalf("expected both user and catalog records to remain, foundUser=%v foundCatalog=%v", foundUser, foundCatalog)
	}
}

func waitForDiscoveredModelEvent(t *testing.T, sub <-chan agent.ExplorerEvent, modelName string) {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub:
			if ev.Type != agent.EventModelDiscovered {
				continue
			}
			if ev.Model == modelName {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for model.discovered event for %s", modelName)
		}
	}
}

func drainExplorerEvents(sub <-chan agent.ExplorerEvent) {
	for {
		select {
		case <-sub:
		default:
			return
		}
	}
}

func assertNoDiscoveredModelEvent(t *testing.T, sub <-chan agent.ExplorerEvent, modelName string) {
	t.Helper()
	timeout := time.After(150 * time.Millisecond)
	for {
		select {
		case ev := <-sub:
			if ev.Type == agent.EventModelDiscovered && ev.Model == modelName {
				t.Fatalf("unexpected duplicate event on second scan: %+v", ev)
			}
		case <-timeout:
			return
		}
	}
}

func writeScanModelFixture(dir string, weightSize int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	config := []byte(`{"model_type":"llama","hidden_size":4096,"num_hidden_layers":32,"num_attention_heads":32}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), config, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "model.safetensors"), make([]byte, weightSize), 0o644)
}
