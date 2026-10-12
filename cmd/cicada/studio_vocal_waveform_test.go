package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/kernel/seq"
)

func TestVocalWaveformUsesMappedVocalRangeAndMonoSignal(t *testing.T) {
	s := newTakeStudio(t, t.TempDir())
	source := []byte(strings.Replace(audioTakeScore, "cicada 2\n", "cicada 2\ntempo 120\n", 1))
	if err := os.WriteFile(s.path, source, 0600); err != nil {
		t.Fatal(err)
	}
	timeline := &takejournal.Timeline{StartTick: seq.TicksPerBar, EndTick: 2 * seq.TicksPerBar, PlaybackStartTick: 0, TempoMilli: 120000}
	id, err := s.takes.BeginTimeline("vox", "session", studioRevision(source), 48000, 2, timeline)
	if err != nil {
		t.Fatal(err)
	}
	var left, right [4096]float32
	// A count-in and a backing pre-roll precede the vocal. The origin is
	// reported input timing, rather than an assumed trim based on raw length.
	for at := 0; at < 288000; {
		n := min(len(left), 288000-at)
		for i := 0; i < n; i++ {
			left[i], right[i] = .99, .99
			if at+i >= 192000 {
				left[i], right[i] = .8, -.4
			}
			if at+i >= 240000 {
				left[i], right[i] = -.8, .2
			}
		}
		b := capture.RecordedBlock{RawFrame: uint64(at), Timing: capture.Block{SampleRate: 48000, Frames: n, Layout: capture.LayoutStereo}, Placement: capture.Placement{EngineFrame: int64(at) - 96000}}
		if err = s.takes.Write(id, b, [][]float32{left[:n], right[:n]}); err != nil {
			t.Fatal(err)
		}
		at += n
	}
	if err = s.takes.Finalize(id, false); err != nil {
		t.Fatal(err)
	}
	if err = s.takes.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, _ := s.takes.Get(id)
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.domainRoutes().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1"+path, nil))
		return w
	}
	path := "/api/takes/" + id + "/waveform?bins=64"
	response := call(path)
	if response.Code != 200 {
		t.Fatalf("waveform %d: %s", response.Code, response.Body.String())
	}
	var wave vocalWaveform
	if err = json.Unmarshal(response.Body.Bytes(), &wave); err != nil {
		t.Fatal(err)
	}
	if wave.Take != id || wave.Start != seq.TicksPerBar || wave.End != 2*seq.TicksPerBar || len(wave.Peaks) != 64 {
		t.Fatalf("waveform %+v", wave)
	}
	for i, p := range wave.Peaks {
		want := float32(.2)
		if i >= 32 {
			want = -.3
		}
		if math.Abs(float64(p[0]-want)) > 1e-6 || math.Abs(float64(p[1]-want)) > 1e-6 {
			t.Fatalf("bin %d=%v want %f", i, p, want)
		}
	}
	if math.Abs(wave.Peak-.3) > 1e-6 {
		t.Fatalf("count-in/pre-roll leaked into peak: %f", wave.Peak)
	}
	if cached := call(path); cached.Body.String() != response.Body.String() {
		t.Fatal("cached waveform changed")
	}
	current, _ := s.takes.Get(id)
	if current.Frames != take.Frames || current.Asset.SHA256 != take.Asset.SHA256 {
		t.Fatal("analysis changed the retained take")
	}
	if strings.Contains(response.Body.String(), take.Asset.Path) {
		t.Fatal("waveform exposed asset path")
	}
	for _, query := range []string{"bins=0", "bins=8193", "bins=bogus"} {
		if r := call("/api/takes/" + id + "/waveform?" + query); r.Code != 400 {
			t.Fatalf("unbounded query %s: %d", query, r.Code)
		}
	}
	if r := call("/api/takes/unknown/waveform"); r.Code != 404 {
		t.Fatalf("unknown take %d", r.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = analyzeVocalWaveform(ctx, takeRoot(s.path), take, 64); err == nil {
		t.Fatal("analysis ignored cancellation")
	}
}
