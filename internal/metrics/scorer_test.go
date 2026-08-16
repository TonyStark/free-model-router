package metrics

import (
	"testing"
)

func TestScoreModelNoHistory(t *testing.T) {
	Default = New()
	// model with no history should get MetadataWeightNoHistory (0.85)
	score := ScoreModel("unknown-model")
	if score != 0.85 {
		t.Errorf("expected 0.85 for unknown model, got %f", score)
	}
}

func TestScoreModelWithHistory(t *testing.T) {
	m := New()
	// 8+ attempts = full confidence, no blending
	m.getOrCreate("good-model").Successes = 10
	m.getOrCreate("good-model").Failures = 0
	m.getOrCreate("good-model").TotalLatMs = 2000
	Default = m

	score := ScoreModel("good-model")
	// sr = 1.0, avg = 200ms, latScore = 1 - 200/30000 = 0.993
	// total=10 >= minAttempts=8, so full formula:
	// score = 0.6*1.0 + 0.3*0.993 + 0.35 = 0.6 + 0.298 + 0.35 = 1.248
	// (Note: score can exceed 1.0 with the new weights — this is expected)
	if score < 1.0 {
		t.Errorf("expected score > 1.0 for perfect model, got %f", score)
	}
}

func TestScoreModelFailuresOnly(t *testing.T) {
	m := New()
	// 8+ attempts = full confidence, no blending
	m.getOrCreate("bad-model").Failures = 10
	Default = m

	score := ScoreModel("bad-model")
	// sr = 0, latScore = 1 (no latency data), total=10 >= 8
	// score = 0.6*0 + 0.3*1 + 0.35 = 0.65
	if score < 0.6 || score > 0.7 {
		t.Errorf("expected score ~0.65, got %f", score)
	}
}

func TestScoreModelBlending(t *testing.T) {
	m := New()
	// 4 attempts = below minModelAttemptsForConfidence (8), so blending occurs
	m.getOrCreate("new-model").Successes = 4
	m.getOrCreate("new-model").Failures = 0
	m.getOrCreate("new-model").TotalLatMs = 1000
	Default = m

	score := ScoreModel("new-model")
	// blend = 4/8 = 0.5, sr=1.0, avg=250ms, latScore=0.992
	// blended = 0.5 * (0.6*1.0 + 0.3*0.992 + 0.35) + 0.5 * 0.85
	//         = 0.5 * 1.2476 + 0.425 = 0.6238 + 0.425 = 1.0488
	if score < 0.85 || score > 1.1 {
		t.Errorf("expected blended score between 0.85 and 1.1, got %f", score)
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
