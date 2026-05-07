package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	state "github.com/jguan/aima/internal"
	"github.com/jguan/aima/internal/knowledge"
	"github.com/jguan/aima/internal/mcp"
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
					Name:   "funasr-paraformer-onnx",
					Type:   "asr",
					Family: "funasr",
				},
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
}

func TestListModelsDoesNotRegisterCatalogLocalModels(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(catalogDir, "configuration.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cat := &knowledge.Catalog{
		ModelAssets: []knowledge.ModelAsset{{
			Metadata: knowledge.ModelMetadata{
				Name:   "funasr-paraformer-onnx",
				Type:   "asr",
				Family: "funasr",
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
