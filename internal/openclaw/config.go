package openclaw

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	aimaLLMProviderID         = "aima"
	legacyLLMProviderID       = "vllm"
	openAIImageProviderID     = "openai"
	localOpenAIAPIKeyFallback = "local"
)

// ReadConfig reads and parses openclaw.json into a generic map.
func ReadConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read openclaw config: %w", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse openclaw config: %w", err)
	}
	return cfg, nil
}

// WriteConfig writes the config map back to openclaw.json with indentation.
func WriteConfig(path string, cfg map[string]any) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal openclaw config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write openclaw config: %w", err)
	}
	return nil
}

// MergeAIMAConfig merges AIMA-generated provider config into the existing
// OpenClaw config using explicit ownership tracked in ManagedState.
func MergeAIMAConfig(existing map[string]any, result *SyncResult) map[string]any {
	merged, _ := MergeAIMAConfigWithState(existing, nil, result)
	return merged
}

func MergeAIMAConfigWithState(existing map[string]any, managed *ManagedState, result *SyncResult) (map[string]any, *ManagedState) {
	if existing == nil {
		existing = make(map[string]any)
	}
	if result == nil {
		return existing, &ManagedState{Version: managedStateVersion}
	}
	next := &ManagedState{Version: managedStateVersion}

	mergeLLMProvider(existing, managed, next, result)
	next.AudioModels = mergeMediaModels(existing, "audio", audioIDs(result.ASRModels), managedSet(managedAudioModels(managed)), result.ProxyAddr, false)
	next.VisionModels = mergeMediaModels(existing, "image", modelIDs(vlmFromLLMs(result.LLMModels)), managedSet(managedVisionModels(managed)), result.ProxyAddr, false)
	mergeTTS(existing, managed, next, result)
	mergeImageGeneration(existing, managed, next, result)

	pruneEmptyMaps(existing)
	normalizeManagedState(next)
	return existing, next
}

func mergeLLMProvider(cfg map[string]any, managed, next *ManagedState, result *SyncResult) {
	providers := ensureMap(ensureMap(cfg, "models"), "providers")
	if len(result.LLMModels) > 0 {
		providers[aimaLLMProviderID] = buildProviderConfig(result.ProxyAddr, result.APIKey, buildLLMProviderModels(result.LLMModels))
		next.LLMProvider = aimaLLMProviderID
	} else {
		delete(providers, aimaLLMProviderID)
	}
	if legacyLLMProviderOwned(managed, cfg, result.ProxyAddr) {
		delete(providers, legacyLLMProviderID)
	}
}

func mergeTTS(cfg map[string]any, managed, next *ManagedState, result *SyncResult) {
	ownsTTS := managedOwnsTTS(managed)
	if result.TTSModel == nil {
		if ownsTTS {
			removeAIMATTS(cfg, managed, result.ProxyAddr)
		}
		return
	}
	if !canManageTTS(cfg, managed) {
		return
	}

	env := ensureMap(cfg, "env")
	env["OPENAI_TTS_BASE_URL"] = result.ProxyAddr
	messages := ensureMap(cfg, "messages")
	messages["tts"] = map[string]any{
		"provider": "openai",
		"openai": map[string]any{
			"apiKey":  directToolAPIKey(result.APIKey),
			"baseUrl": result.ProxyAddr,
			"model":   result.TTSModel.ID,
			"voice":   "default",
		},
	}
	next.TTSModel = result.TTSModel.ID
}

func canManageTTS(cfg map[string]any, managed *ManagedState) bool {
	if managedOwnsTTS(managed) {
		return true
	}
	return lookupMap(cfg, "messages", "tts") == nil
}

func mergeImageGeneration(cfg map[string]any, managed, next *ManagedState, result *SyncResult) {
	ownsImageGen := managedOwnsImageGeneration(managed)
	if len(result.ImageGenModels) == 0 {
		if ownsImageGen {
			removeImageGeneration(cfg, managed)
		}
		return
	}
	if !canManageImageGeneration(cfg, managed) {
		return
	}

	providers := ensureMap(ensureMap(cfg, "models"), "providers")
	providers[openAIImageProviderID] = buildProviderConfig(result.ProxyAddr, directToolAPIKey(result.APIKey), buildImageGenProviderModels(result.ImageGenModels))
	setAgentDefaultModel(cfg, "imageGenerationModel", openAIImageProviderID, imageGenIDs(result.ImageGenModels))
	next.ImageGenerationProvider = openAIImageProviderID
	next.ImageGenerationModels = imageGenIDs(result.ImageGenModels)
}

func canManageImageGeneration(cfg map[string]any, managed *ManagedState) bool {
	if managedOwnsImageGeneration(managed) {
		return true
	}
	return !hasAgentDefaultModel(cfg, "imageGenerationModel") && lookupMap(cfg, "models", "providers", openAIImageProviderID) == nil
}

func removeImageGeneration(cfg map[string]any, managed *ManagedState) {
	if !managedOwnsImageGeneration(managed) {
		return
	}
	removeAgentDefaultModelIfManaged(cfg, "imageGenerationModel", managed.ImageGenerationProvider)
	removeProviderIfPresent(cfg, managed.ImageGenerationProvider)
}

func legacyLLMProviderOwned(managed *ManagedState, cfg map[string]any, proxyAddr string) bool {
	if managed != nil && managed.LLMProvider == legacyLLMProviderID {
		return true
	}
	return providerManagedByAIMA(lookupMap(cfg, "models", "providers", legacyLLMProviderID), proxyAddr)
}

func managedAudioModels(managed *ManagedState) []string {
	if managed == nil {
		return nil
	}
	return managed.AudioModels
}

func managedVisionModels(managed *ManagedState) []string {
	if managed == nil {
		return nil
	}
	return managed.VisionModels
}

func buildProviderConfig(baseURL, apiKey string, models []any) map[string]any {
	cfg := map[string]any{
		"baseUrl": baseURL,
		"api":     "openai-completions",
		"models":  models,
	}
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		cfg["apiKey"] = trimmed
	}
	return cfg
}

func buildLLMProviderModels(models []ModelEntry) []any {
	out := make([]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{
			"id":            m.ID,
			"name":          m.Name,
			"input":         m.Input,
			"contextWindow": m.ContextWindow,
			"maxTokens":     m.MaxTokens,
			"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		})
	}
	return out
}

func buildImageGenProviderModels(models []ImageGenEntry) []any {
	out := make([]any, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]any{
			"id":            m.ID,
			"name":          formatDisplayName(m.ID, "image_gen"),
			"input":         []string{"text"},
			"contextWindow": 8192,
			"maxTokens":     1024,
			"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
		})
	}
	return out
}

func modelIDs(models []ModelEntry) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func audioIDs(models []AudioEntry) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func imageGenIDs(models []ImageGenEntry) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func mergeMediaModels(cfg map[string]any, key string, desired []string, owned map[string]struct{}, proxyAddr string, allowLegacy bool) []string {
	existing := lookupMap(cfg, "tools", "media")
	section := map[string]any(nil)
	if existing != nil {
		section = copyMap(existing[key])
	}
	preserved := keepUnmanagedMediaModels(section, owned, proxyAddr, allowLegacy)
	if len(desired) == 0 && len(preserved) == 0 {
		removeMediaSection(cfg, key)
		return nil
	}

	tools := ensureMap(cfg, "tools")
	media := ensureMap(tools, "media")
	if section == nil {
		section = make(map[string]any)
	}
	section["enabled"] = true
	models := make([]any, 0, len(desired)+len(preserved))
	for _, id := range desired {
		models = append(models, map[string]any{
			"provider": "openai",
			"model":    id,
			"baseUrl":  proxyAddr,
		})
	}
	models = append(models, preserved...)
	section["models"] = models
	media[key] = section
	return desired
}

func keepUnmanagedMediaModels(section map[string]any, owned map[string]struct{}, proxyAddr string, allowLegacy bool) []any {
	if section == nil {
		return nil
	}
	models, ok := section["models"].([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(models))
	for _, raw := range models {
		entry, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		if isManagedMediaEntry(entry, owned, proxyAddr, allowLegacy) {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func isManagedMediaEntry(entry map[string]any, owned map[string]struct{}, proxyAddr string, allowLegacy bool) bool {
	if owned != nil {
		if _, ok := owned[asString(entry["model"])]; ok {
			return true
		}
	}
	if normalizeURL(asString(entry["baseUrl"])) != normalizeURL(proxyAddr) {
		return false
	}
	return allowLegacy
}

func removeMediaSection(cfg map[string]any, key string) {
	tools, ok := cfg["tools"].(map[string]any)
	if !ok {
		return
	}
	media, ok := tools["media"].(map[string]any)
	if !ok {
		return
	}
	delete(media, key)
}

func removeAIMATTS(cfg map[string]any, managed *ManagedState, proxyAddr string) {
	if managedOwnsTTS(managed) || currentTTSManagedByAIMA(cfg, proxyAddr) {
		if messages, ok := cfg["messages"].(map[string]any); ok {
			delete(messages, "tts")
		}
	}
	if env, ok := cfg["env"].(map[string]any); ok {
		if managedOwnsTTS(managed) || normalizeURL(asString(env["OPENAI_TTS_BASE_URL"])) == normalizeURL(proxyAddr) {
			delete(env, "OPENAI_TTS_BASE_URL")
		}
	}
}

func currentTTSManagedByAIMA(cfg map[string]any, proxyAddr string) bool {
	openai := lookupMap(cfg, "messages", "tts", "openai")
	if openai == nil {
		return false
	}
	if normalizeURL(asString(openai["baseUrl"])) == normalizeURL(proxyAddr) {
		return true
	}
	env, _ := cfg["env"].(map[string]any)
	return normalizeURL(asString(env["OPENAI_TTS_BASE_URL"])) == normalizeURL(proxyAddr)
}

func providerManagedByAIMA(provider map[string]any, proxyAddr string) bool {
	if provider == nil {
		return false
	}
	return normalizeURL(asString(provider["baseUrl"])) == normalizeURL(proxyAddr)
}

func directToolAPIKey(apiKey string) string {
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		return trimmed
	}
	return localOpenAIAPIKeyFallback
}

func copyMap(raw any) map[string]any {
	src, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func lookupAgentDefault(cfg map[string]any, key string) any {
	defaults := lookupMap(cfg, "agents", "defaults")
	if defaults == nil {
		return nil
	}
	return defaults[key]
}

func hasAgentDefaultModel(cfg map[string]any, key string) bool {
	defaults := lookupMap(cfg, "agents", "defaults")
	if defaults == nil {
		return false
	}
	_, ok := defaults[key]
	return ok
}

func setAgentDefaultModel(cfg map[string]any, key, providerID string, modelIDs []string) {
	if len(modelIDs) == 0 {
		return
	}
	defaults := ensureMap(ensureMap(cfg, "agents"), "defaults")
	refs := make([]string, 0, len(modelIDs))
	for _, id := range modelIDs {
		refs = append(refs, providerID+"/"+id)
	}
	value := map[string]any{"primary": refs[0]}
	if len(refs) > 1 {
		fallbacks := make([]any, 0, len(refs)-1)
		for _, ref := range refs[1:] {
			fallbacks = append(fallbacks, ref)
		}
		value["fallbacks"] = fallbacks
	}
	defaults[key] = value
}

func removeAgentDefaultModelIfManaged(cfg map[string]any, key, providerID string) {
	agents, ok := cfg["agents"].(map[string]any)
	if !ok {
		return
	}
	defaults, ok := agents["defaults"].(map[string]any)
	if !ok {
		return
	}
	refs := parseAgentModelRefs(defaults[key])
	if len(refs) == 0 {
		delete(defaults, key)
		return
	}
	for _, ref := range refs {
		if ref.Provider != providerID {
			return
		}
	}
	delete(defaults, key)
}

func removeProviderIfPresent(cfg map[string]any, providerID string) {
	models, ok := cfg["models"].(map[string]any)
	if !ok {
		return
	}
	providers, ok := models["providers"].(map[string]any)
	if !ok {
		return
	}
	delete(providers, providerID)
}

func vlmFromLLMs(models []ModelEntry) []ModelEntry {
	var vlms []ModelEntry
	for _, m := range models {
		for _, inp := range m.Input {
			if inp == "image" {
				vlms = append(vlms, m)
				break
			}
		}
	}
	return vlms
}

func ensureMap(cfg map[string]any, key string) map[string]any {
	v, ok := cfg[key].(map[string]any)
	if !ok {
		v = make(map[string]any)
		cfg[key] = v
	}
	return v
}

func pruneEmptyMaps(cfg map[string]any) {
	prunePath(cfg, "models", "providers")
	prunePath(cfg, "models")
	prunePath(cfg, "tools", "media")
	prunePath(cfg, "tools")
	prunePath(cfg, "messages")
	prunePath(cfg, "env")
	prunePath(cfg, "agents", "defaults")
	prunePath(cfg, "agents")
}

func prunePath(cfg map[string]any, keys ...string) {
	if len(keys) == 0 {
		return
	}
	if len(keys) == 1 {
		if child, ok := cfg[keys[0]].(map[string]any); ok && len(child) == 0 {
			delete(cfg, keys[0])
		}
		return
	}
	child, ok := cfg[keys[0]].(map[string]any)
	if !ok {
		return
	}
	prunePath(child, keys[1:]...)
	if len(child) == 0 {
		delete(cfg, keys[0])
	}
}
