package metrics

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	Default = New()
	os.Exit(m.Run())
}

func TestRecordSuccess(t *testing.T) {
	m := New()
	m.RecordSuccess("model-a", "hint1", 100)
	m.RecordSuccess("model-a", "hint1", 200)

	s := m.ModelStats["model-a"]
	if s == nil {
		t.Fatal("expected ModelStats for model-a")
	}
	if s.Successes != 2 {
		t.Errorf("expected 2 successes, got %d", s.Successes)
	}
	if s.TotalLatMs != 300 {
		t.Errorf("expected TotalLatMs=300, got %d", s.TotalLatMs)
	}
	if s.LastUsed == 0 {
		t.Error("expected LastUsed to be set")
	}
}

func TestRecordSuccessWithKeyStat(t *testing.T) {
	m := New()
	m.RegisterKey("hint1", 1)
	m.RecordSuccess("model-a", "hint1", 50)

	ks := m.KeyStats["hint1"]
	if ks == nil {
		t.Fatal("expected KeyStat for hint1")
	}
	if ks.Successes != 1 {
		t.Errorf("expected 1 key success, got %d", ks.Successes)
	}
}

func TestRecordFailure(t *testing.T) {
	m := New()
	m.RecordFailure("model-a", "")
	m.RecordFailure("model-a", "")

	s := m.ModelStats["model-a"]
	if s == nil {
		t.Fatal("expected ModelStats for model-a")
	}
	if s.Failures != 2 {
		t.Errorf("expected 2 failures, got %d", s.Failures)
	}
}

func TestRecordFailureWithKeyStat(t *testing.T) {
	m := New()
	m.RegisterKey("hint1", 1)
	m.RecordFailure("model-a", "hint1")

	ks := m.KeyStats["hint1"]
	if ks == nil {
		t.Fatal("expected KeyStat for hint1")
	}
	if ks.Failures != 1 {
		t.Errorf("expected 1 key failure, got %d", ks.Failures)
	}
}

func TestSuccessRate(t *testing.T) {
	m := New()
	m.getOrCreate("model-a").Successes = 3
	m.getOrCreate("model-a").Failures = 1

	rate := m.SuccessRate("model-a")
	if rate != 0.75 {
		t.Errorf("expected 0.75, got %f", rate)
	}
}

func TestSuccessRateUnknown(t *testing.T) {
	m := New()
	if rate := m.SuccessRate("unknown"); rate != -1 {
		t.Errorf("expected -1 for unknown model, got %f", rate)
	}
}

func TestSuccessRateNoData(t *testing.T) {
	m := New()
	m.getOrCreate("empty").Successes = 0
	m.getOrCreate("empty").Failures = 0

	if rate := m.SuccessRate("empty"); rate != -1 {
		t.Errorf("expected -1 for model with no data, got %f", rate)
	}
}

func TestAvgLatMs(t *testing.T) {
	m := New()
	m.getOrCreate("model-a").Successes = 2
	m.getOrCreate("model-a").TotalLatMs = 500

	avg := m.AvgLatMs("model-a")
	if avg != 250 {
		t.Errorf("expected 250, got %f", avg)
	}
}

func TestAvgLatMsUnknown(t *testing.T) {
	m := New()
	if avg := m.AvgLatMs("unknown"); avg != 0 {
		t.Errorf("expected 0 for unknown model, got %f", avg)
	}
}

func TestAvgLatMsNoSuccesses(t *testing.T) {
	m := New()
	m.getOrCreate("model-a").Failures = 5

	if avg := m.AvgLatMs("model-a"); avg != 0 {
		t.Errorf("expected 0 for model with no successes, got %f", avg)
	}
}

func TestRegisterKey(t *testing.T) {
	m := New()
	m.RegisterKey("hint1", 1)
	m.RegisterKey("hint2", 2)
	m.RegisterKey("hint1", 3) // duplicate — should be ignored

	if len(m.KeyStats) != 2 {
		t.Errorf("expected 2 key stats, got %d", len(m.KeyStats))
	}
	if m.KeyStats["hint1"].Number != 1 {
		t.Errorf("expected hint1 number=1, got %d", m.KeyStats["hint1"].Number)
	}
}

func TestSummary(t *testing.T) {
	m := New()
	m.TotalRequests.Add(10)
	m.TotalSuccesses.Add(7)
	m.TotalErrors.Add(3)
	m.ActiveRequests.Store(2)
	m.StreamRequests.Store(1)

	s := m.Summary()
	expected := "total=10 ok=7 err=3 active=2 streams=1"
	if s != expected {
		t.Errorf("expected %q, got %q", expected, s)
	}
}

func TestGetOrCreate(t *testing.T) {
	m := New()
	s1 := m.getOrCreate("model-a")
	s2 := m.getOrCreate("model-a")
	if s1 != s2 {
		t.Error("getOrCreate should return the same pointer for the same model")
	}
	s3 := m.getOrCreate("model-b")
	if s1 == s3 {
		t.Error("getOrCreate should return a different pointer for a different model")
	}
}

// --- Package-level convenience function tests ---

func resetDefault() {
	Default = New()
}

func TestIncTotalRequests(t *testing.T) {
	resetDefault()
	IncTotalRequests()
	if Default.TotalRequests.Load() != 1 {
		t.Errorf("expected 1, got %d", Default.TotalRequests.Load())
	}
}

func TestIncActiveRequests(t *testing.T) {
	resetDefault()
	IncActiveRequests()
	IncActiveRequests()
	if Default.ActiveRequests.Load() != 2 {
		t.Errorf("expected 2, got %d", Default.ActiveRequests.Load())
	}
}

func TestDecActiveRequests(t *testing.T) {
	resetDefault()
	Default.ActiveRequests.Store(5)
	DecActiveRequests()
	if Default.ActiveRequests.Load() != 4 {
		t.Errorf("expected 4, got %d", Default.ActiveRequests.Load())
	}
}

func TestActiveCount(t *testing.T) {
	resetDefault()
	Default.ActiveRequests.Store(3)
	if c := ActiveCount(); c != 3 {
		t.Errorf("expected 3, got %d", c)
	}
}

func TestIncTotalErrors(t *testing.T) {
	resetDefault()
	IncTotalErrors()
	IncTotalErrors()
	if Default.TotalErrors.Load() != 2 {
		t.Errorf("expected 2, got %d", Default.TotalErrors.Load())
	}
}

func TestIncTotalSuccesses(t *testing.T) {
	resetDefault()
	IncTotalSuccesses()
	if Default.TotalSuccesses.Load() != 1 {
		t.Errorf("expected 1, got %d", Default.TotalSuccesses.Load())
	}
}

func TestIncStreamRequests(t *testing.T) {
	resetDefault()
	IncStreamRequests()
	if Default.StreamRequests.Load() != 1 {
		t.Errorf("expected 1, got %d", Default.StreamRequests.Load())
	}
}

func TestPkgRecordSuccess(t *testing.T) {
	resetDefault()
	Default.RegisterKey("hint1", 1)
	RecordSuccess("model-pkg", "hint1", 100)

	s := Default.ModelStats["model-pkg"]
	if s == nil || s.Successes != 1 {
		t.Errorf("expected 1 success, got %+v", s)
	}
	ks := Default.KeyStats["hint1"]
	if ks == nil || ks.Successes != 1 {
		t.Errorf("expected 1 key success, got %+v", ks)
	}
}

func TestPkgRecordFailure(t *testing.T) {
	resetDefault()
	Default.RegisterKey("hint1", 1)
	RecordFailure("model-fail", "hint1")

	s := Default.ModelStats["model-fail"]
	if s == nil || s.Failures != 1 {
		t.Errorf("expected 1 failure, got %+v", s)
	}
}

func TestPkgAvgLatMs(t *testing.T) {
	resetDefault()
	Default.RecordSuccess("lat-model", "hint1", 300)
	Default.RecordSuccess("lat-model", "hint1", 500)

	avg := AvgLatMs("lat-model")
	if avg != 400 {
		t.Errorf("expected 400, got %f", avg)
	}
}

func TestPkgRegisterKey(t *testing.T) {
	resetDefault()
	RegisterKey("new-key", 42)
	ks := Default.KeyStats["new-key"]
	if ks == nil || ks.Number != 42 {
		t.Errorf("expected key 42, got %+v", ks)
	}
}

func TestPkgSummary(t *testing.T) {
	resetDefault()
	Default.TotalRequests.Store(5)
	s := Summary()
	if s != "total=5 ok=0 err=0 active=0 streams=0" {
		t.Errorf("unexpected summary: %s", s)
	}
}
