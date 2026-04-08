package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jguan/aima/catalog"
	"github.com/jguan/aima/internal/cli"
	"github.com/jguan/aima/internal/knowledge"
)

const defaultOnboardingSampleModel = "qwen3-8b"

func buildOnboardingManifestJSON(cat *knowledge.Catalog) (json.RawMessage, error) {
	raw, err := catalog.FS.ReadFile("ui-onboarding.json")
	if err != nil {
		return nil, err
	}

	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}

	root := cli.NewRootCmd(&cli.App{})
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	fillTopLevelOnboardingCommands(manifest, root)

	out, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}

	sampleModel := pickOnboardingSampleModel(cat)
	return json.RawMessage(replaceSampleModelPlaceholder(string(out), sampleModel)), nil
}

func fillTopLevelOnboardingCommands(manifest map[string]any, root *cobra.Command) {
	locales, ok := manifest["locales"].(map[string]any)
	if !ok {
		return
	}

	for _, localeValue := range locales {
		locale, ok := localeValue.(map[string]any)
		if !ok {
			continue
		}

		fullCommands, ok := locale["full_commands"].(map[string]any)
		if !ok {
			continue
		}

		groups, ok := fullCommands["groups"].([]any)
		if !ok {
			continue
		}

		for _, groupValue := range groups {
			group, ok := groupValue.(map[string]any)
			if !ok || strings.TrimSpace(stringValue(group["id"])) != "top_level_commands" {
				continue
			}
			group["items"] = buildTopLevelOnboardingItems(root, topLevelCommandDescriptions(group["items"]))
		}
	}
}

func topLevelCommandDescriptions(raw any) map[string]string {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}

	descriptions := make(map[string]string, len(items))
	for _, itemValue := range items {
		item, ok := itemValue.(map[string]any)
		if !ok {
			continue
		}

		id := strings.TrimSpace(stringValue(item["id"]))
		description := strings.TrimSpace(stringValue(item["description"]))
		if id == "" || description == "" {
			continue
		}
		descriptions[id] = description
	}
	return descriptions
}

func buildTopLevelOnboardingItems(root *cobra.Command, descriptions map[string]string) []map[string]any {
	if root == nil {
		return nil
	}

	items := make([]map[string]any, 0, len(root.Commands()))
	for _, cmd := range root.Commands() {
		if cmd == nil || cmd.Hidden {
			continue
		}

		description := strings.TrimSpace(descriptions[cmd.Name()])
		if description == "" {
			description = strings.TrimSpace(cmd.Short)
		}
		if description == "" {
			description = strings.TrimSpace(cmd.Long)
		}

		items = append(items, map[string]any{
			"id":          cmd.Name(),
			"command":     "/cli " + cmd.Name(),
			"description": description,
		})
	}
	return items
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func replaceSampleModelPlaceholder(value, sampleModel string) string {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(sampleModel) == "" {
		return value
	}

	replacer := strings.NewReplacer(
		"`{sample_model}`", sampleModel,
		"{sample_model}", sampleModel,
		" is an example value and can be replaced with a real model name.", " is an example model name; replace it with your own model name.",
		" 是示例值，可替换为实际模型名。", " 是示例模型名，可替换成你自己的模型名。",
	)
	return replacer.Replace(value)
}

func pickOnboardingSampleModel(cat *knowledge.Catalog) string {
	if cat == nil {
		return defaultOnboardingSampleModel
	}

	bestName := ""
	bestScore := math.MaxFloat64
	for _, asset := range cat.ModelAssets {
		if !strings.EqualFold(strings.TrimSpace(asset.Metadata.Type), "llm") {
			continue
		}

		score := parseModelParameterCount(asset.Metadata.ParameterCount)
		if bestName == "" || score < bestScore {
			bestName = asset.Metadata.Name
			bestScore = score
		}
	}
	if bestName != "" {
		return bestName
	}

	for _, asset := range cat.ModelAssets {
		if strings.TrimSpace(asset.Metadata.Name) != "" {
			return asset.Metadata.Name
		}
	}
	return defaultOnboardingSampleModel
}

func parseModelParameterCount(raw string) float64 {
	value := strings.TrimSpace(strings.ToUpper(raw))
	if value == "" {
		return math.MaxFloat64
	}

	multiplier := 1.0
	switch {
	case strings.HasSuffix(value, "B"):
		value = strings.TrimSuffix(value, "B")
	case strings.HasSuffix(value, "M"):
		value = strings.TrimSuffix(value, "M")
		multiplier = 0.001
	case strings.HasSuffix(value, "K"):
		value = strings.TrimSuffix(value, "K")
		multiplier = 0.000001
	}

	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return math.MaxFloat64
	}
	return number * multiplier
}
