package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CleanupLegacyAgentModelCatalogs removes stale agent-local provider catalogs
// that still point OpenClaw's legacy image-generation wiring at the AIMA proxy.
func CleanupLegacyAgentModelCatalogs(stateDir, proxyAddr string, imageGenModels []ImageGenEntry) error {
	if stringsTrimmedEmpty(stateDir) || stringsTrimmedEmpty(proxyAddr) || len(imageGenModels) == 0 {
		return nil
	}

	agentsDir := filepath.Join(stateDir, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read openclaw agents dir: %w", err)
	}

	legacyModels := managedSet(imageGenIDs(imageGenModels))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		modelsPath := filepath.Join(agentsDir, entry.Name(), "agent", "models.json")
		if err := cleanupLegacyAgentModelsFile(modelsPath, proxyAddr, legacyModels); err != nil {
			return err
		}
	}
	return nil
}

func cleanupLegacyAgentModelsFile(path, proxyAddr string, legacyModels map[string]struct{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read agent models cache %s: %w", path, err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse agent models cache %s: %w", path, err)
	}

	providers, ok := cfg["providers"].(map[string]any)
	if !ok || len(providers) == 0 {
		return nil
	}

	changed := false
	for providerID, raw := range providers {
		provider, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if normalizeURL(asString(provider["baseUrl"])) != normalizeURL(proxyAddr) {
			continue
		}
		if !providerCatalogMatchesOnlyModels(provider, legacyModels) {
			continue
		}
		delete(providers, providerID)
		changed = true
	}

	if !changed {
		return nil
	}

	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cleaned agent models cache %s: %w", path, err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0644); err != nil {
		return fmt.Errorf("write cleaned agent models cache %s: %w", path, err)
	}
	return nil
}

func providerCatalogMatchesOnlyModels(provider map[string]any, allowed map[string]struct{}) bool {
	models, ok := provider["models"].([]any)
	if !ok || len(models) == 0 || len(allowed) == 0 {
		return false
	}
	for _, raw := range models {
		model, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := allowed[asString(model["id"])]; !ok {
			return false
		}
	}
	return true
}

func stringsTrimmedEmpty(value string) bool {
	return asString(value) == ""
}
