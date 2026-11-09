package main

import (
	"encoding/json"
	"testing"

	"m31labs.dev/cicada/host/liveplay"
	"m31labs.dev/cicada/kernel/analysis"
)

func TestStudioAnalysisJSONShape(t *testing.T) {
	snapshot := liveplay.AnalysisSnapshot{Sequence: 4, Correlation: .5, HasCorrelation: true, HasSpectrum: true, SampleRate: 48000}
	snapshot.Spectrum[10] = -12
	raw, err := json.Marshal(studioAnalysis(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Correlation *float64 `json:"correlation"`
		Spectrum    struct {
			LowHz  float64   `json:"low_hz"`
			HighHz float64   `json:"high_hz"`
			Bands  []float64 `json:"bands_db"`
		} `json:"spectrum"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Correlation == nil || *got.Correlation != .5 {
		t.Fatalf("correlation %v, want 0.5", got.Correlation)
	}
	if got.Spectrum.LowHz != 20 || got.Spectrum.HighHz != 20000 || len(got.Spectrum.Bands) != analysis.Bands || got.Spectrum.Bands[10] != -12 {
		t.Fatalf("spectrum %+v", got.Spectrum)
	}

	snapshot.HasCorrelation = false
	raw, _ = json.Marshal(studioAnalysis(snapshot))
	var nullable map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nullable); err != nil {
		t.Fatal(err)
	}
	if string(nullable["correlation"]) != "null" {
		t.Fatalf("correlation = %s, want null", nullable["correlation"])
	}
}

func TestStudioAnalysisAbsentWithoutData(t *testing.T) {
	if studioAnalysis(liveplay.AnalysisSnapshot{}) != nil {
		t.Fatal("empty snapshot produced an analysis object")
	}
	if studioAnalysis(liveplay.AnalysisSnapshot{Correlation: 1, HasCorrelation: true}) != nil {
		t.Fatal("snapshot without a spectrum produced an analysis object")
	}
	// The existing meter message is unchanged by the analysis helper.
	if _, ok := studioMeters(liveplay.MeterFrame{})["analysis"]; ok {
		t.Fatal("studioMeters must not add the analysis key itself")
	}
}
