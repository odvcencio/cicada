package agentsuite

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// brightnessMargin is how much the bass's spectral centroid must rise: a
// quarter, well above the 5% the catalog calls a change in brightness.
const brightnessMargin = 1.25

// peakCeilingDBFS is the sample peak the edited score may reach without a
// loudness target, and the true-peak ceiling render holds with one.
const peakCeilingDBFS = -1.0

// TestAgentBrighterBassTask is the task "make this bass patch brighter without
// clipping", done the way an agent does it: it reads what the bass track
// accepts and which control measurably brightens it from explain, checks the
// range with params, sets the control with setparam through apply, and
// verifies the result with audition and render. It reads only JSON and never
// edits the score's text itself.
func TestAgentBrighterBassTask(t *testing.T) {
	bin := os.Getenv("CICADA_BIN")
	if bin == "" {
		if os.Getenv("CICADA_AGENT_SUITE") == "1" {
			t.Fatal("CICADA_AGENT_SUITE=1 needs CICADA_BIN, the built cicada binary")
		}
		t.Skip("set CICADA_BIN to the built binary; make test-agent-surface does")
	}
	bin, err := filepath.Abs(bin)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	dir := t.TempDir()
	original, err := os.ReadFile(filepath.Join("..", "..", "examples", "keys", "fm_bass.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "song.cicada"), original, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. What does the bass track play, and which of its controls brighten it?
	type direction struct {
		Property string    `json:"property"`
		Change   string    `json:"change"`
		Basis    string    `json:"basis"`
		At       []float64 `json:"at"`
		Measured []float64 `json:"measured"`
	}
	type control struct {
		ID         string      `json:"id"`
		Owner      string      `json:"owner"`
		Name       string      `json:"name"`
		Unit       string      `json:"unit"`
		Min        *float64    `json:"min"`
		Max        *float64    `json:"max"`
		SetIn      []string    `json:"set_in"`
		Value      any         `json:"value"`
		Directions []direction `json:"directions"`
	}
	var track struct {
		Schema string `json:"schema"`
		Track  struct {
			Track     string    `json:"track"`
			Owner     string    `json:"owner"`
			OwnerKind string    `json:"owner_kind"`
			Controls  []control `json:"controls"`
		} `json:"track"`
	}
	stdout, code := cicada(t, bin, dir, "explain", "track:keys", "song.cicada", "--json")
	decode(t, "explain track:keys --json", stdout, &track)
	if code != 0 || track.Schema != "cicada.explain.v1" || track.Track.Owner != "fm_bass" || track.Track.OwnerKind != "patch" {
		t.Fatalf("explain track:keys: exit %d %+v", code, track.Track.Owner)
	}

	// 2. Pick the control whose measured brightness rises most for the least
	// change in level, at the value the probe measured it.
	var chosen control
	var target, bestRatio float64
	for _, c := range track.Track.Controls {
		if c.Owner != track.Track.Owner || !slices.Contains(c.SetIn, "track") {
			continue
		}
		var bright, level *direction
		for i := range c.Directions {
			switch c.Directions[i].Property {
			case "brightness":
				bright = &c.Directions[i]
			case "level":
				level = &c.Directions[i]
			}
		}
		if bright == nil || bright.Basis != "measured" || bright.Change != "up" || len(bright.Measured) != 3 || level == nil || len(level.Measured) != 3 {
			continue
		}
		if level.Measured[2]-level.Measured[1] > 1 {
			continue // louder too: closer to clipping
		}
		if ratio := bright.Measured[2] / bright.Measured[1]; ratio > bestRatio {
			chosen, target, bestRatio = c, bright.At[2], ratio
		}
	}
	if chosen.ID == "" {
		t.Fatal("explain lists no control of the bass that measurably brightens it without raising its level")
	}
	t.Logf("chosen %s = %g (measured centroid ratio %.2f)", chosen.ID, target, bestRatio)

	// 3. Confirm the value lies in the control's range.
	var params struct {
		Schema   string    `json:"schema"`
		Controls []control `json:"controls"`
	}
	stdout, code = cicada(t, bin, dir, "params", "--json", "--voice", track.Track.Owner, chosen.ID)
	decode(t, "params --json --voice", stdout, &params)
	if code != 0 || params.Schema != "cicada.params.v1" || len(params.Controls) != 1 || params.Controls[0].Min == nil || target < *params.Controls[0].Min || target > *params.Controls[0].Max {
		t.Fatalf("params --json --voice %s %s: exit %d %+v", track.Track.Owner, chosen.ID, code, params)
	}

	// 4. Measure the starting point.
	type auditionReport struct {
		Format string `json:"format"`
		Tracks []struct {
			ID         string  `json:"id"`
			RMSdBFS    float64 `json:"rms_dbfs"`
			CentroidHz float64 `json:"centroid_hz"`
		} `json:"tracks"`
	}
	listen := func(what string) (float64, float64) {
		t.Helper()
		var report auditionReport
		stdout, code := cicada(t, bin, dir, "audition", "song.cicada", "--bars", "4", "--json")
		decode(t, what, stdout, &report)
		for _, tr := range report.Tracks {
			if tr.ID == "keys" && code == 0 {
				return tr.CentroidHz, tr.RMSdBFS
			}
		}
		t.Fatalf("%s: exit %d, no keys track", what, code)
		return 0, 0
	}
	beforeCentroid, beforeLevel := listen("audition before")

	// 5. A value outside the range is refused with the range as data.
	type applyResult struct {
		OK    bool `json:"ok"`
		Error *struct {
			Reason string `json:"reason"`
			Field  string `json:"field"`
			Range  *struct {
				Parameter string  `json:"parameter"`
				Max       float64 `json:"max"`
			} `json:"range"`
		} `json:"error"`
	}
	edit := func(value float64) applyResult {
		t.Helper()
		envelope, err := json.Marshal(map[string]any{"version": 1, "intents": []any{map[string]any{"kind": "setparam", "entity": "param:keys." + chosen.Name, "value": value}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "edit.json"), envelope, 0o644); err != nil {
			t.Fatal(err)
		}
		var result applyResult
		stdout, _ := cicada(t, bin, dir, "apply", "song.cicada", "edit.json", "--write", "--json")
		decode(t, "apply --json", stdout, &result)
		return result
	}
	refused := edit(*params.Controls[0].Max + 1)
	if refused.OK || refused.Error == nil || refused.Error.Reason != "value_out_of_range" || refused.Error.Field != "value" || refused.Error.Range == nil || refused.Error.Range.Parameter != chosen.Name || refused.Error.Range.Max != *params.Controls[0].Max {
		t.Fatalf("an out-of-range value: %+v", refused.Error)
	}

	// 6. Set the control.
	if result := edit(target); !result.OK {
		t.Fatalf("setparam %s = %g: %+v", chosen.Name, target, result.Error)
	}
	edited, err := os.ReadFile(filepath.Join(dir, "song.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(edited, original) {
		t.Fatal("apply --write did not change the score")
	}

	// 7. Brighter, by the stated margin, and not louder by more than 1.5 dB.
	afterCentroid, afterLevel := listen("audition after")
	t.Logf("centroid %.0f Hz -> %.0f Hz, level %.1f dBFS -> %.1f dBFS", beforeCentroid, afterCentroid, beforeLevel, afterLevel)
	if afterCentroid < beforeCentroid*brightnessMargin {
		t.Fatalf("the bass is not brighter by %.0f%%: %.0f Hz -> %.0f Hz", (brightnessMargin-1)*100, beforeCentroid, afterCentroid)
	}
	if afterLevel-beforeLevel > 1.5 {
		t.Fatalf("the bass got louder by %.1f dB", afterLevel-beforeLevel)
	}

	// 8. No clipping: the raw render stays under the ceiling, and a delivery
	// at -14 LUFS meets its true-peak ceiling.
	var raw struct {
		OK     bool `json:"ok"`
		Levels *struct {
			OutputPeak      float64 `json:"output_peak"`
			ClippedCount    int64   `json:"clipped_count"`
			PreLimiterOvers int64   `json:"pre_limiter_overs"`
		} `json:"levels"`
	}
	stdout, code = cicada(t, bin, dir, "render", "song.cicada", "-o", "raw.wav", "--bars", "4", "--json")
	decode(t, "render --json", stdout, &raw)
	if code != 0 || !raw.OK || raw.Levels == nil || raw.Levels.ClippedCount != 0 || raw.Levels.PreLimiterOvers != 0 || 20*math.Log10(raw.Levels.OutputPeak) > peakCeilingDBFS {
		t.Fatalf("render --json: exit %d %+v", code, raw.Levels)
	}
	var delivered struct {
		OK       bool `json:"ok"`
		Loudness *struct {
			TruePeakMaxDBTP float64  `json:"true_peak_max_dbtp"`
			TruePeakDBTP    *float64 `json:"true_peak_dbtp"`
			TargetMet       bool     `json:"target_met"`
		} `json:"loudness"`
	}
	stdout, code = cicada(t, bin, dir, "render", "song.cicada", "-o", "master.wav", "--bars", "4", "--loudness", "-14", "--true-peak-max", "-1", "--json")
	decode(t, "render --loudness --json", stdout, &delivered)
	if code != 0 || !delivered.OK || delivered.Loudness == nil || !delivered.Loudness.TargetMet || delivered.Loudness.TruePeakDBTP == nil || *delivered.Loudness.TruePeakDBTP > delivered.Loudness.TruePeakMaxDBTP {
		t.Fatalf("render --loudness -14 --json: exit %d %+v", code, delivered.Loudness)
	}
	elapsed := time.Since(started)
	t.Logf("brighter bass task: %s", elapsed.Round(time.Millisecond))
	if elapsed > suiteBudget {
		t.Fatalf("the task took %s; the suite's budget is %s", elapsed, suiteBudget)
	}
}
