package metrics

import (
	"testing"
)

func TestScoreModelNoHistory(t *testing.T) {
	Default = New()
	// model with no history should get default score (sr < 0, latScore based on 0 avg)
	score := ScoreModel("unknown-model")
	if score <= 0 {
		t.Errorf("expected positive score for unknown model, got %f", score)
	}
}

func TestScoreModelWithHistory(t *testing.T) {
	m := New()
	m.getOrCreate("good-model").Successes = 4
	m.getOrCreate("good-model").Failures = 0
	m.getOrCreate("good-model").TotalLatMs = 2000
	Default = m

	score := ScoreModel("good-model")
	// success rate = 1.0, latScore = 1 - 2000/30000/4 ≈ 1 - 0.0167 = 0.983
	// score = 0.6*1.0 + 0.3*0.983 + 0.1 = 0.6 + 0.295 + 0.1 = 0.995
	if score < 0.9 || score > 1.1 {
		t.Errorf("expected score ~0.995, got %f", score)
	}
}

func TestScoreModelFailuresOnly(t *testing.T) {
	m := New()
	m.getOrCreate("bad-model").Failures = 3
	Default = m

	score := ScoreModel("bad-model")
	// sr = 0, latScore = 1 (no latency data)
	// score = 0.6*0 + 0.3*1 + 0.1 = 0.4
	if score < 0.35 || score > 0.45 {
		t.Errorf("expected score ~0.4, got %f", score)
	}
}

func TestHasHistory(t *testing.T) {
	m := New()
	Default = m

	if HasHistory("unknown") {
		t.Error("expected false for model with no history")
	}

	m.getOrCreate("known").Successes = 1
	if !HasHistory("known") {
		t.Error("expected true for model with history")
	}
}

func TestGetPriorityDefaults(t *testing.T) {
	// With default config (no priority keywords), all models get 999
	if p := GetPriority("gpt-4o"); p != 999 {
		t.Errorf("expected 999 with default config, got %d", p)
	}
}

func TestSortByScore(t *testing.T) {
	m := New()
	Default = m

	// model-a: high success rate and low latency
	m.getOrCreate("model-a").Successes = 50
	m.getOrCreate("model-a").Failures = 2
	m.getOrCreate("model-a").TotalLatMs = 30000

	// model-b: moderate
	m.getOrCreate("model-b").Successes = 30
	m.getOrCreate("model-b").Failures = 10
	m.getOrCreate("model-b").TotalLatMs = 60000

	// model-c: poor
	m.getOrCreate("model-c").Successes = 5
	m.getOrCreate("model-c").Failures = 20
	m.getOrCreate("model-c").TotalLatMs = 300000

	sorted := SortByScore([]string{"model-c", "model-b", "model-a"})
	if sorted[0] != "model-a" {
		t.Errorf("expected model-a first, got %s", sorted[0])
	}
	if sorted[2] != "model-c" {
		t.Errorf("expected model-c last, got %s", sorted[2])
	}
}

func TestSortByScorePreservesInput(t *testing.T) {
	m := New()
	Default = m

	original := []string{"z-model", "a-model", "m-model"}
	sorted := SortByScore(original)

	if len(sorted) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(sorted))
	}
	if original[0] != "z-model" || original[1] != "a-model" || original[2] != "m-model" {
		t.Error("SortByScore should not modify the original slice")
	}
}
