package soundprobe

import (
	"bytes"
	"os"
	"strings"
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

// sampleIDs is what every pull request probes: the sample above and one
// control of every other owner the probe measures. The middle of a probe's
// three values is the default, so that one control renders the owner's
// default sound and a change to any voice or patch fails here, not only in
// the nightly run.
func sampleIDs() []string {
	catalog := controls.All()
	ids := append([]string(nil), sample...)
	seen := map[string]bool{}
	for _, id := range sample {
		c, _ := catalog.Control(id)
		seen[c.Owner] = true
	}
	for _, id := range ProbeableIDs() {
		c, _ := catalog.Control(id)
		if !seen[c.Owner] {
			seen[c.Owner] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// TestMeasuredDirectionsMatchTheTable probes a sample of controls and
// requires the checked-in table to hold the same measurements and therefore
// the same directions. CICADA_PROBE_ALL=1 probes every probeable control
// (the nightly run). CICADA_UPDATE_DIRECTIONS=1 probes every probeable
// control and rewrites internal/controls/directions.json; a comma-separated
// list of owners or ID prefixes (CICADA_UPDATE_DIRECTIONS=fm_bass,acid.cutoff)
// probes only those and keeps the other lines, so a change to one voice
// regenerates only that voice's lines.
func TestMeasuredDirectionsMatchTheTable(t *testing.T) {
	started := time.Now()
	if update := os.Getenv("CICADA_UPDATE_DIRECTIONS"); update != "" {
		probeable := ProbeableIDs()
		table := controls.Measured()
		ids := probeable
		if update == "1" {
			table.Controls = map[string]controls.ProbedControl{}
		} else {
			ids = nil
			for _, id := range probeable {
				for _, prefix := range strings.Split(update, ",") {
					if prefix = strings.TrimSpace(prefix); prefix != "" && strings.HasPrefix(id, prefix) {
						ids = append(ids, id)
						break
					}
				}
			}
			if len(ids) == 0 {
				t.Fatalf("CICADA_UPDATE_DIRECTIONS=%s selects no probeable control", update)
			}
		}
		keep := map[string]bool{}
		for _, id := range probeable {
			keep[id] = true
		}
		for id := range table.Controls {
			if !keep[id] {
				delete(table.Controls, id)
			}
		}
		results := ProbeAll(ids, 0)
		for _, r := range results {
			if r.Err != nil {
				t.Errorf("%s: %v", r.ID, r.Err)
				delete(table.Controls, r.ID)
				continue
			}
			table.Controls[r.ID] = r.Probe
		}
		fresh := Table(nil)
		table.Schema, table.Phrase = fresh.Schema, fresh.Phrase
		data, err := table.Encode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(tablePath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("probed %d controls (%d renders) in %s", len(results), 3*len(results), time.Since(started).Round(time.Millisecond))
		return
	}
	table := controls.Measured()
	ids := sampleIDs()
	if os.Getenv("CICADA_PROBE_ALL") == "1" {
		ids = ProbeableIDs()
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

// TestTableCoversEveryProbeableControl renders nothing: it fails when a
// control was added, removed, or had its range or default changed since the
// table was generated, because the table then measures the wrong values.
// Changes to how a control sounds are caught by the sample above on every
// pull request and by CICADA_PROBE_ALL=1 for all of them.
func TestTableCoversEveryProbeableControl(t *testing.T) {
	const regenerate = "; regenerate with CICADA_UPDATE_DIRECTIONS=1 go test ./host/soundprobe -run TestMeasuredDirectionsMatchTheTable"
	table := controls.Measured()
	catalog := controls.All()
	probeable := map[string]bool{}
	for _, id := range ProbeableIDs() {
		probeable[id] = true
		held, ok := table.Controls[id]
		if !ok {
			t.Errorf("%s is probeable but not in the table%s", id, regenerate)
			continue
		}
		c, _ := catalog.Control(id)
		values, _ := Values(c)
		if len(held.At) != 3 || held.At[0] != values[0] || held.At[1] != values[1] || held.At[2] != values[2] {
			t.Errorf("%s: the table measured it at %v, the probe now uses %v%s", id, held.At, values, regenerate)
		}
	}
	for id := range table.Controls {
		if !probeable[id] {
			t.Errorf("the table holds %s, which the probe no longer measures%s", id, regenerate)
		}
	}
	for kind, text := range Phrases {
		if table.Phrase[kind] != text {
			t.Errorf("the table's %s phrase is %q, the probe's %q%s", kind, table.Phrase[kind], text, regenerate)
		}
	}
	if table.Phrase["method"] != Method {
		t.Errorf("the table's method is %q, the probe's %q%s", table.Phrase["method"], Method, regenerate)
	}
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
