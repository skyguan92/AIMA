package proxy

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// AdvertisedModel is the minimal model metadata needed for agent-side selection.
type AdvertisedModel struct {
	ID                  string `json:"id"`
	ParameterCount      string `json:"parameter_count,omitempty"`
	ContextWindowTokens int    `json:"context_window_tokens,omitempty"`
	Remote              bool   `json:"remote,omitempty"`
}

var sizeTokenPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*([bm])\b`)

// SortAdvertisedModels orders models for agent selection: stronger first, then
// larger context windows, then local before remote, then name for stability.
func SortAdvertisedModels(models []AdvertisedModel) {
	sort.SliceStable(models, func(i, j int) bool {
		return BetterAdvertisedModel(models[i], models[j])
	})
}

// BestAdvertisedModel returns the highest-priority model from the slice.
func BestAdvertisedModel(models []AdvertisedModel) (AdvertisedModel, bool) {
	if len(models) == 0 {
		return AdvertisedModel{}, false
	}
	best := models[0]
	for _, candidate := range models[1:] {
		if BetterAdvertisedModel(candidate, best) {
			best = candidate
		}
	}
	return best, true
}

// BetterAdvertisedModel reports whether a should be preferred over b.
func BetterAdvertisedModel(a, b AdvertisedModel) bool {
	aScore := modelStrengthScore(a.ID, a.ParameterCount)
	bScore := modelStrengthScore(b.ID, b.ParameterCount)
	if aScore != bScore {
		return aScore > bScore
	}
	if a.ContextWindowTokens != b.ContextWindowTokens {
		return a.ContextWindowTokens > b.ContextWindowTokens
	}
	if a.Remote != b.Remote {
		return !a.Remote && b.Remote
	}
	return strings.ToLower(strings.TrimSpace(a.ID)) < strings.ToLower(strings.TrimSpace(b.ID))
}

func modelStrengthScore(modelID, parameterCount string) float64 {
	if score := parseParameterCountScore(parameterCount); score > 0 {
		return score
	}
	return parseParameterCountScore(modelID)
}

func parseParameterCountScore(raw string) float64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	matches := sizeTokenPattern.FindAllStringSubmatch(strings.ToUpper(raw), -1)
	best := 0.0
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil || value <= 0 {
			continue
		}
		switch match[2] {
		case "M":
			value /= 1000
		case "B":
			// already in billions
		default:
			continue
		}
		if value > best {
			best = value
		}
	}
	if strings.Contains(strings.ToUpper(raw), "<1B") {
		return 0.999
	}
	return best
}
