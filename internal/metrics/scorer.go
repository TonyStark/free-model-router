package metrics

import (
	"math"
	"sort"
	"strings"

	"free-model-router/internal/config"
)

func GetPriority(model string) int {
	cfg := config.Get()
	openrouterCfg, ok := cfg.Providers["openrouter"]
	if !ok {
		return 999
	}
	modelLower := strings.ToLower(model)
	for i, kw := range openrouterCfg.PriorityKeywords {
		if strings.Contains(modelLower, strings.ToLower(kw)) {
			return i
		}
	}
	return 999
}

func ScoreModel(model string) float64 {
	cfg := config.Get()
	sr := Default.SuccessRate(model)
	avg := Default.AvgLatMs(model)
	latScore := math.Max(0, 1.0-avg/30000.0)
	if sr < 0 {
		return cfg.Global.MetadataWeightNoHistory
	}
	total := Default.TotalAttempts(model)
	if total < int64(cfg.Global.MinModelAttemptsForConfidence) {
		blend := float64(total) / float64(cfg.Global.MinModelAttemptsForConfidence)
		return blend*(0.6*sr + 0.3*latScore + cfg.Global.MetadataWeightWithHistory) +
			(1-blend)*cfg.Global.MetadataWeightNoHistory
	}
	return 0.6*sr + 0.3*latScore + cfg.Global.MetadataWeightWithHistory
}

func HasHistory(model string) bool { return Default.SuccessRate(model) >= 0 }

func SortByScore(models []string) []string {
	ranked := make([]string, len(models))
	copy(ranked, models)
	sort.SliceStable(ranked, func(i, j int) bool {
		pI := GetPriority(ranked[i])
		pJ := GetPriority(ranked[j])
		if pI != pJ {
			return pI < pJ
		}
		return ScoreModel(ranked[i]) > ScoreModel(ranked[j])
	})
	return ranked
}
