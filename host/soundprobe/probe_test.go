package soundprobe

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"m31labs.dev/cicada/internal/controls"
)

const tablePath = "../../internal/controls/directions.json"

// sample is the set CI probes on every run: every operator level of the FM
// bass (the agent task steers by them), a keyboard patch of each other
// family, a built-in voice and a library patch.
var sample = []string{
	"fm_bass.op1_level", "fm_bass.op2_level", "fm_bass.op3_level", "fm_bass.op4_level", "fm_bass.op5_level", "fm_bass.op6_level",
	"fm_bass.gain", "tine_ep.hammer_felt", "clav.string_mute", "tonewheel_organ.drawbar3", "poly_keys.cutoff",
	"string_machine.attack", "acid.cutoff", "drums.bd_decay", "round-bass.brightness",
}

// TestMeasuredDirectionsMatchTheTable probes a sample of controls and
// requires the checked-in table to hold the same measurements and therefore
// the same directions. CICADA_PROBE_ALL=1 probes every control in the table
// (the nightly run); CICADA_UPDATE_DIRECTIONS=1 probes every probeable
// control and rewrites internal/controls/directions.json.
func TestMeasuredDirectionsMatchTheTable(t *testing.T) {
	started := time.Now()
	if os.Getenv("CICADA_UPDATE_DIRECTIONS") == "1" {
		ids := ProbeableIDs()
		results := ProbeAll(ids, 0)
		for _, r := range results {
			if r.Err != nil {
				t.Errorf("%s: %v", r.ID, r.Err)
			}
		}
		data, err := json.MarshalIndent(Table(results), "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tablePath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("probed %d controls (%d renders) in %s", len(results), 3*len(results), time.Since(started).Round(time.Millisecond))
		return
	}
	table := controls.Measured()
	ids := sample
	if os.Getenv("CICADA_PROBE_ALL") == "1" {
		ids = nil
		for id := range table.Controls {
			ids = append(ids, id)
		}
		probeable := map[string]bool{}
		for _, id := range ProbeableIDs() {
			probeable[id] = true
			if _, ok := table.Controls[id]; !ok {
				t.Errorf("%s is probeable but not in the table; run CICADA_UPDATE_DIRECTIONS=1 go test ./host/soundprobe", id)
			}
		}
		for id := range table.Controls {
			if !probeable[id] {
				t.Errorf("the table holds %s, which the probe no longer measures", id)
			}
		}
	}
	for _, r := range ProbeAll(ids, 0) {
		if r.Err != nil {
			t.Errorf("%s: %v", r.ID, r.Err)
			continue
		}
		want, ok := table.Controls[r.ID]
		if !ok {
			t.Errorf("%s is not in the table; run CICADA_UPDATE_DIRECTIONS=1 go test ./host/soundprobe", r.ID)
			continue
		}
		if !Same(r.Probe, want) {
			t.Errorf("%s measures %+v, the table holds %+v", r.ID, r.Probe, want)
			continue
		}
		got, held := r.Probe.Directions(), want.Directions()
		for i := range got {
			if got[i].Change != held[i].Change {
				t.Errorf("%s %s: measured %s, the table says %s", r.ID, got[i].Property, got[i].Change, held[i].Change)
			}
		}
	}
	t.Logf("probed %d controls in %s", len(ids), time.Since(started).Round(time.Millisecond))
}

// A stated direction is certain from the signal path; the probe must agree
// wherever it measured the same control.
func TestStatedDirectionsAgreeWithMeasurements(t *testing.T) {
	checked := 0
	for _, c := range controls.All().Controls {
		for _, d := range c.Directions {
			if d.Basis != controls.BasisStated || len(d.Measured) == 0 {
				continue
			}
			checked++
			if d.Change != controls.ChangeUp {
				t.Errorf("%s: stated %s up, measured %s (%v at %v)", c.ID, d.Property, d.Change, d.Measured, d.At)
			}
		}
	}
	t.Logf("%d stated directions verified by measurement", checked)
}

func TestProbeScoresParse(t *testing.T) {
	var buf bytes.Buffer
	catalog := controls.All()
	for _, id := range ProbeableIDs() {
		c, _ := catalog.Control(id)
		owner, _ := catalog.Owner(c.Owner)
		values, _ := Values(c)
		source, _, err := score(owner, c, values[1])
		if err != nil {
			t.Fatal(err)
		}
		buf.WriteString(source)
	}
	if buf.Len() == 0 {
		t.Fatal("no probe scores")
	}
	t.Logf("%d probeable controls, %d renders for the full table", len(ProbeableIDs()), 3*len(ProbeableIDs()))
}
