package measure

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Metric names, as they appear in the JSON and in testdata/tolerances.json.
const (
	metricAliasing = "aliasing_db"
	metricTHDN     = "thdn_db"
	metricResponse = "frequency_response_error_db"
	metricNoise    = "noise_floor_dbfs"
	metricCPU      = "cpu_ns_per_block"
)

var metricOrder = []string{metricAliasing, metricTHDN, metricResponse, metricNoise, metricCPU}

const (
	statusMeasured = "measured"
	statusNA       = "not_applicable"
)

type point struct {
	Case       string  `json:"case"`
	Hz         float64 `json:"hz"`
	ExpectedDB float64 `json:"expected_db"`
	MeasuredDB float64 `json:"measured_db"`
	ErrorDB    float64 `json:"error_db"`
}

type metric struct {
	Status string   `json:"status"`
	Unit   string   `json:"unit,omitempty"`
	Value  *float64 `json:"value,omitempty"`
	// Gated is true when the test fails if Value exceeds the limit in
	// testdata/tolerances.json. Limit echoes that limit for the reader.
	Gated  bool               `json:"gated"`
	Limit  *float64           `json:"limit,omitempty"`
	Reason string             `json:"reason,omitempty"`
	Detail map[string]float64 `json:"detail,omitempty"`
	Points []point            `json:"points,omitempty"`
}

type entry struct {
	Name        string             `json:"name"`
	Kind        string             `json:"kind"`
	Description string             `json:"description"`
	Metrics     map[string]*metric `json:"metrics"`
}

type report struct {
	Schema     int      `json:"schema"`
	SampleRate int      `json:"sample_rate_hz"`
	GoVersion  string   `json:"go_version"`
	Platform   string   `json:"platform"`
	CPUNote    string   `json:"cpu_note"`
	ElapsedSec float64  `json:"elapsed_seconds"`
	Entries    []*entry `json:"entries"`
}

func newEntry(name, kind, description string) *entry {
	return &entry{Name: name, Kind: kind, Description: description, Metrics: map[string]*metric{}}
}

func (e *entry) measured(name, unit string, value float64, gated bool) *metric {
	value = round(value)
	m := &metric{Status: statusMeasured, Unit: unit, Value: &value, Gated: gated}
	e.Metrics[name] = m
	return m
}

func (e *entry) notApplicable(name, reason string) {
	e.Metrics[name] = &metric{Status: statusNA, Reason: reason}
}

func round(x float64) float64 { return math.Round(x*1000) / 1000 }

func newReport() *report {
	return &report{
		Schema: 1, SampleRate: sampleRate, GoVersion: runtime.Version(),
		Platform: runtime.GOOS + "/" + runtime.GOARCH,
		CPUNote:  "cpu_ns_per_block is informational and varies by machine and load; it is never gated",
	}
}

// ---- tolerances ----

type tolerance struct {
	Limit    float64 `json:"limit"`
	Measured float64 `json:"measured_when_set"`
}

type toleranceFile struct {
	Schema int    `json:"schema"`
	Rule   string `json:"rule"`
	// Limits maps entry name -> metric name -> limit.
	Limits map[string]map[string]tolerance `json:"limits"`
}

const toleranceRule = "limit = measured + margin, rounded up to 0.1 (see README.md): " +
	"aliasing_db and thdn_db +3 dB with a floor of -110 dB; noise_floor_dbfs +3 dB with a floor of -120 dBFS; " +
	"frequency_response_error_db 1.25x + 0.05 dB. A metric fails when its value is above its limit."

func toleranceFor(metricName string, measured float64) float64 {
	var limit float64
	switch metricName {
	case metricAliasing, metricTHDN:
		limit = math.Max(measured+3, -110)
	case metricNoise:
		limit = math.Max(measured+3, -120)
	case metricResponse:
		limit = measured*1.25 + .05
	default:
		panic("no tolerance rule for " + metricName)
	}
	return math.Ceil(limit*10-1e-9) / 10
}

func tolerancePath() string { return filepath.Join("testdata", "tolerances.json") }

func loadTolerances() (*toleranceFile, error) {
	data, err := os.ReadFile(tolerancePath())
	if err != nil {
		return nil, err
	}
	var f toleranceFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func buildTolerances(r *report) *toleranceFile {
	f := &toleranceFile{Schema: 1, Rule: toleranceRule, Limits: map[string]map[string]tolerance{}}
	for _, e := range r.Entries {
		for name, m := range e.Metrics {
			if m.Status != statusMeasured || !m.Gated {
				continue
			}
			if f.Limits[e.Name] == nil {
				f.Limits[e.Name] = map[string]tolerance{}
			}
			f.Limits[e.Name][name] = tolerance{Limit: toleranceFor(name, *m.Value), Measured: *m.Value}
		}
	}
	return f
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// gate returns one message per gated metric that is worse than its limit,
// missing a limit, or has a limit without a measurement. It also fills in
// each metric's Limit field.
func gate(r *report, tol *toleranceFile) []string {
	var failures []string
	seen := map[string]map[string]bool{}
	for _, e := range r.Entries {
		for _, name := range metricOrder {
			m := e.Metrics[name]
			if m == nil {
				failures = append(failures, fmt.Sprintf("%s: no %s entry (every entry must report every metric or say not_applicable)", e.Name, name))
				continue
			}
			if m.Status != statusMeasured || !m.Gated {
				continue
			}
			t, ok := tol.Limits[e.Name][name]
			if !ok {
				failures = append(failures, fmt.Sprintf("%s: %s has no limit in tolerances.json (measured %.2f); set one deliberately, see README.md", e.Name, name, *m.Value))
				continue
			}
			if seen[e.Name] == nil {
				seen[e.Name] = map[string]bool{}
			}
			seen[e.Name][name] = true
			limit := t.Limit
			m.Limit = &limit
			if math.IsNaN(*m.Value) || *m.Value > limit {
				failures = append(failures, fmt.Sprintf("%s: %s = %.2f is worse than its limit %.2f", e.Name, name, *m.Value, limit))
			}
		}
	}
	var stale []string
	for entryName, metrics := range tol.Limits {
		for name := range metrics {
			if !seen[entryName][name] {
				stale = append(stale, fmt.Sprintf("%s: tolerances.json has a limit for %s but the suite measured no gated value", entryName, name))
			}
		}
	}
	sort.Strings(stale)
	return append(failures, stale...)
}
