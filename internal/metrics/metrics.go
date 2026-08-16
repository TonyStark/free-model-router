package metrics

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"free-model-router/internal/logger"
)

type ModelStats struct {
	Successes  int64 `json:"successes"`
	Failures   int64 `json:"failures"`
	TotalLatMs int64 `json:"total_lat_ms"`
	LastUsed   int64 `json:"last_used"`
}

type KeyStat struct {
	Number    int
	Successes int64
	Failures  int64
}

type Metrics struct {
	TotalRequests  atomic.Int64
	ActiveRequests atomic.Int64
	TotalErrors    atomic.Int64
	TotalSuccesses atomic.Int64
	StreamRequests atomic.Int64
	Mu             sync.RWMutex
	ModelStats     map[string]*ModelStats
	KeyStats       map[string]*KeyStat // hint → stat
}

var Default *Metrics

func New() *Metrics {
	return &Metrics{
		ModelStats: make(map[string]*ModelStats),
		KeyStats:   make(map[string]*KeyStat),
	}
}

func (m *Metrics) LoadScoreCache(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var loaded map[string]*ModelStats
	if err := json.Unmarshal(data, &loaded); err == nil {
		for k, v := range loaded {
			m.ModelStats[k] = v
		}
		logger.Debug("Score cache loaded: %d model(s) from %s", len(loaded), path)
	}
}

func (m *Metrics) SaveScoreCache(path string) {
	m.Mu.RLock()
	data, err := json.MarshalIndent(m.ModelStats, "", "  ")
	m.Mu.RUnlock()
	if err == nil {
		os.WriteFile(path, data, 0644)
	}
}

func (m *Metrics) getOrCreate(model string) *ModelStats {
	if s, ok := m.ModelStats[model]; ok {
		return s
	}
	s := &ModelStats{}
	m.ModelStats[model] = s
	return s
}

func (m *Metrics) RegisterKey(hint string, number int) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if _, ok := m.KeyStats[hint]; !ok {
		m.KeyStats[hint] = &KeyStat{Number: number}
	}
}

func (m *Metrics) RecordSuccess(model, hint string, latMs int64) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	s := m.getOrCreate(model)
	s.Successes++
	s.TotalLatMs += latMs
	s.LastUsed = time.Now().Unix()
	if hint != "" {
		if ks, ok := m.KeyStats[hint]; ok {
			ks.Successes++
		}
	}
}

func (m *Metrics) RecordFailure(model, hint string) {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.getOrCreate(model).Failures++
	if hint != "" {
		if ks, ok := m.KeyStats[hint]; ok {
			ks.Failures++
		}
	}
}

func (m *Metrics) SuccessRate(model string) float64 {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	s, ok := m.ModelStats[model]
	if !ok {
		return -1
	}
	total := s.Successes + s.Failures
	if total == 0 {
		return -1
	}
	return float64(s.Successes) / float64(total)
}

func (m *Metrics) AvgLatMs(model string) float64 {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	s, ok := m.ModelStats[model]
	if !ok || s.Successes == 0 {
		return 0
	}
	return float64(s.TotalLatMs) / float64(s.Successes)
}

func (m *Metrics) TotalAttempts(model string) int64 {
	m.Mu.RLock()
	defer m.Mu.RUnlock()
	s, ok := m.ModelStats[model]
	if !ok {
		return 0
	}
	return s.Successes + s.Failures
}

func (m *Metrics) Summary() string {
	return fmt.Sprintf("total=%d ok=%d err=%d active=%d streams=%d",
		m.TotalRequests.Load(), m.TotalSuccesses.Load(),
		m.TotalErrors.Load(), m.ActiveRequests.Load(),
		m.StreamRequests.Load())
}

// --- package-level convenience functions (delegate to Default) ---

func IncTotalRequests()  { Default.TotalRequests.Add(1) }
func IncActiveRequests() { Default.ActiveRequests.Add(1) }
func DecActiveRequests() { Default.ActiveRequests.Add(-1) }
func ActiveCount() int64 { return Default.ActiveRequests.Load() }
func IncTotalErrors()    { Default.TotalErrors.Add(1) }
func IncTotalSuccesses() { Default.TotalSuccesses.Add(1) }
func IncStreamRequests() { Default.StreamRequests.Add(1) }

func RecordSuccess(model, hint string, latMs int64) { Default.RecordSuccess(model, hint, latMs) }
func RecordFailure(model, hint string)               { Default.RecordFailure(model, hint) }
func AvgLatMs(model string) float64                  { return Default.AvgLatMs(model) }
func RegisterKey(hint string, number int)            { Default.RegisterKey(hint, number) }
func Summary() string                                { return Default.Summary() }
