// Package soundprobe measures which way each sound control moves what a
// listener hears. For one control it renders a fixed short phrase at three
// values (low, default and high) and reads the listening report
// (host/scoreaudition) for the track: level, brightness, attack and decay.
// The results are checked in as internal/controls/directions.json, which the
// control catalog turns into the measured directions cicada params and
// cicada explain show.
package soundprobe

import (
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"m31labs.dev/cicada/host/scoreaudition"
	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/notation"
)

// Phrases are the fixed phrases the probe renders, by what plays them. Every
// phrase is two bars at 120 BPM, so a probe renders four seconds.
var Phrases = map[string]string{
	"notes": "two bars of c3 and g3, each held three sixteenths and followed by five sixteenths of rest",
	"acid":  "two bars of degrees 1 (accented) and 5, each held three sixteenths and followed by five of rest",
	"drums": "two bars hitting each of the eleven lanes once (bd sd ch oh cp rs lt mt ht cb cy)",
	"kit":   "two bars of a model.kick lane hit on beats one and three",
}

// Method describes how the three values are chosen, for the table.
const Method = "low and high are half and twice the default for times, frequencies and ratios, otherwise the default minus and plus a quarter of the range, kept inside the range; an instrument parameter without a range takes half and twice its default, or plus and minus 0.25 when it lies within 0..1"

// Probeable reports whether the probe measures a control: a number of a voice,
// keyboard patch, library patch or kit lane, other than the octave.
func Probeable(owner controls.Owner, c controls.Control) bool {
	switch owner.Kind {
	case controls.KindVoice, controls.KindPatch, controls.KindLibraryPatch, controls.KindKitLane:
	default:
		return false
	}
	if c.Type != "number" && c.Type != "integer" || c.Name == "octave" || c.Name == "experimental" {
		return false
	}
	if owner.Kind == controls.KindKitLane && c.Name == "pan" {
		return false
	}
	_, ok := Values(c)
	return ok
}

// Values chooses the low, default and high values the probe renders.
func Values(c controls.Control) ([3]float64, bool) {
	def, ok := c.Default.(float64)
	if !ok {
		return [3]float64{}, false
	}
	var lo, hi float64
	relative := c.Unit == "Hz" || c.Unit == "ms" || strings.Contains(c.Name, "ratio")
	switch {
	case c.Min == nil || c.Max == nil:
		if def > 1 || relative || def < 0 {
			lo, hi = def/2, def*2
		} else {
			lo, hi = math.Max(0, def-.25), math.Min(1, def+.25)
		}
	case relative && *c.Min >= 0 && def > 0:
		lo, hi = math.Max(*c.Min, def/2), math.Min(*c.Max, def*2)
	default:
		span := *c.Max - *c.Min
		// The compiler turns span/4 into a product it may fuse with the add
		// on arm64; rounding it keeps the probe values the same everywhere.
		quarter := float64(span / 4)
		lo, hi = math.Max(*c.Min, def-quarter), math.Min(*c.Max, def+quarter)
	}
	if c.Type == "integer" {
		lo, hi = math.Round(lo), math.Round(hi)
		for _, v := range c.Excluded {
			if hi == v {
				hi = math.Min(*c.Max, hi+1)
			}
			if lo == v {
				lo = math.Max(*c.Min, lo-1)
			}
		}
	}
	lo, hi = roundProbe(lo), roundProbe(hi)
	if lo == def && hi == def {
		return [3]float64{}, false
	}
	return [3]float64{lo, def, hi}, true
}

// roundProbe keeps four significant digits, so the score text stays short.
// strconv rounds the decimal correctly, so every target gets the same bits;
// math.Pow and math.Log10 would not (kernel/dsp/detmath's guard).
func roundProbe(v float64) float64 {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(v, 'g', 4, 64), 64)
	if err != nil {
		return v
	}
	return rounded
}

// score writes the probe score for one control at one value.
func score(owner controls.Owner, c controls.Control, v float64) (string, string, error) {
	const head = "cicada 2\ntitle \"probe\"\ntempo 120\nkey c minor\nseed 1\n"
	setting := c.Name + " = " + controls.Literal(c, v)
	notes := "pattern p notes { c3 - - . . . . . g3 - - . . . . . }\nscene s { t = p }\nsong { s*2 }\n"
	switch owner.Kind {
	case controls.KindVoice:
		switch owner.Name {
		case "acid":
			return head + "track t acid { " + setting + " }\npattern p { 1^ - - . . . . . 5 - - . . . . . }\nscene s { t = p }\nsong { s*2 }\n", "acid", nil
		case "drums":
			return head + "track t drums { " + setting + " }\n" + drumPhrase, "drums", nil
		case "guitar":
			return head + "track t guitar { experimental = on\n  " + setting + " }\n" + notes, "notes", nil
		}
		return head + "track t " + owner.Name + " { " + setting + " }\n" + notes, "notes", nil
	case controls.KindPatch:
		return head + "track t " + owner.Name + " { " + setting + " }\n" + notes, "notes", nil
	case controls.KindLibraryPatch:
		patch, ok := instrument.FindPatch(owner.Name)
		if !ok {
			return "", "", fmt.Errorf("unknown library patch %s", owner.Name)
		}
		source, err := patch.Source("probe")
		if err != nil {
			return "", "", err
		}
		return head + source + "track t probe { " + setting + " }\n" + notes, "notes", nil
	case controls.KindKitLane:
		return head + "kit k {\n  bd = model.kick\n}\ntrack t k { bd_" + setting + " }\npattern p drums { bd: x... .... x... .... }\nscene s { t = p }\nsong { s*2 }\n", "kit", nil
	}
	return "", "", fmt.Errorf("the probe does not measure %s controls", owner.Kind)
}

const drumPhrase = `pattern p drums {
  bd: x... .... .... ....
  sd: ..x. .... .... ....
  ch: .... x... .... ....
  oh: .... .x.. .... ....
  cp: .... ...x .... ....
  rs: .... .... x... ....
  lt: .... .... .x.. ....
  mt: .... .... ..x. ....
  ht: .... .... ...x ....
  cb: .... .... .... x...
  cy: .... .... .... ..x.
}
scene s { t = p }
song { s*2 }
`

// Measurement is the listening report's view of the probe track.
type Measurement struct {
	LevelDB, CentroidHz, AttackMS, DecayMS float64
}

// Measure renders the probe score for a control at one value and reads the
// probe track's report.
func Measure(owner controls.Owner, c controls.Control, v float64) (Measurement, error) {
	source, _, err := score(owner, c, v)
	if err != nil {
		return Measurement{}, err
	}
	parsed, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return Measurement{}, fmt.Errorf("%s = %g: %s: %s", c.ID, v, d.Code, d.Message)
		}
	}
	analysis, err := scoreaudition.Analyze(parsed, scoreaudition.Options{})
	if err != nil {
		return Measurement{}, fmt.Errorf("%s = %g: %w", c.ID, v, err)
	}
	for _, track := range analysis.Report.Tracks {
		if track.ID == "t" {
			return Measurement{LevelDB: track.RMSdBFS, CentroidHz: track.CentroidHz, AttackMS: track.AttackMs, DecayMS: track.Decay20Ms}, nil
		}
	}
	return Measurement{}, fmt.Errorf("%s = %g: the report has no probe track", c.ID, v)
}

// Probe measures one control at its three values.
func Probe(owner controls.Owner, c controls.Control) (controls.ProbedControl, error) {
	values, ok := Values(c)
	if !ok {
		return controls.ProbedControl{}, fmt.Errorf("%s has no probe values", c.ID)
	}
	p := controls.ProbedControl{At: values[:]}
	for _, v := range values {
		m, err := Measure(owner, c, v)
		if err != nil {
			return controls.ProbedControl{}, err
		}
		p.LevelDB = append(p.LevelDB, m.LevelDB)
		p.CentroidHz = append(p.CentroidHz, m.CentroidHz)
		p.AttackMS = append(p.AttackMS, m.AttackMS)
		p.DecayMS = append(p.DecayMS, m.DecayMS)
	}
	return p, nil
}

// Result is one control's probe, or why it failed.
type Result struct {
	ID    string
	Probe controls.ProbedControl
	Err   error
}

// ProbeAll measures the given controls with workers goroutines (0 uses every
// CPU) and returns the results in ID order.
func ProbeAll(ids []string, workers int) []Result {
	catalog := controls.All()
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	jobs := make(chan string)
	results := make([]Result, 0, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				c, _ := catalog.Control(id)
				owner, _ := catalog.Owner(c.Owner)
				probe, err := Probe(owner, c)
				mu.Lock()
				results = append(results, Result{ID: id, Probe: probe, Err: err})
				mu.Unlock()
			}
		}()
	}
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results
}

// ProbeableIDs lists every control the probe measures, in catalog order.
func ProbeableIDs() []string {
	catalog := controls.All()
	var ids []string
	for _, c := range catalog.Controls {
		owner, _ := catalog.Owner(c.Owner)
		if Probeable(owner, c) {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// Table builds the directions table from probe results.
func Table(results []Result) controls.MeasuredTable {
	table := controls.MeasuredTable{Schema: controls.MeasuredSchema, Phrase: map[string]string{"method": Method}, Controls: map[string]controls.ProbedControl{}}
	for kind, text := range Phrases {
		table.Phrase[kind] = text
	}
	for _, r := range results {
		if r.Err == nil {
			table.Controls[r.ID] = r.Probe
		}
	}
	return table
}

// Same compares two probes within the listening report's rounding: the
// render is bit-identical everywhere, and the analysis rounds its numbers.
func Same(a, b controls.ProbedControl) bool {
	near := func(x, y []float64, tolerance float64) bool {
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if math.Abs(x[i]-y[i]) > tolerance {
				return false
			}
		}
		return true
	}
	return near(a.At, b.At, 1e-9) && near(a.LevelDB, b.LevelDB, .05) && near(a.CentroidHz, b.CentroidHz, 1.5) && near(a.AttackMS, b.AttackMS, .5) && near(a.DecayMS, b.DecayMS, .5)
}
