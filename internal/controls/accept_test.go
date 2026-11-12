package controls_test

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/internal/controls"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// fakeHash is a well-formed SHA-256 pin. Building a project checks its form;
// only loading the file compares it with the bytes.
const fakeHash = "0000000000000000000000000000000000000000000000000000000000000000"

// placement writes one setting of a control into a minimal score that puts
// the control's owner where the language allows it.
func placement(c controls.Control, owner controls.Owner, literal string) string {
	const head = "cicada 2\ntempo 120\n"
	notes := "pattern p notes { c3 . e3 . }\nscene s { t = p }\nsong { s }\n"
	drums := "pattern p drums steps=1 { bd: x }\nscene s { t = p }\nsong { s }\n"
	setting := c.Name + " = " + literal
	switch owner.Kind {
	case controls.KindMix:
		if c.Edition == 1 {
			return strings.Replace(placement(controls.Control{Name: c.Name, Owner: c.Owner}, owner, literal), "cicada 2", "cicada 1", 1)
		}
		switch c.Name {
		case "send.delay":
			return head + "fx echo delay {}\ntrack t drums { send echo = " + literal + " }\n" + drums
		case "send.reverb":
			return head + "fx room reverb {}\ntrack t drums { send room = " + literal + " }\n" + drums
		case "send_pre":
			return head + "fx echo delay {}\ntrack t drums { send_a = 0.5\n send_pre = " + literal + " }\n" + drums
		case "insert":
			if literal == "drive" || literal == "none" {
				return head + "fx drive drive {}\ntrack t drums { insert = " + literal + " }\n" + drums
			}
		}
		if strings.HasPrefix(c.Name, "amp_") || strings.HasPrefix(c.Name, "chorus_") {
			kind := "ampcab"
			if strings.HasPrefix(c.Name, "chorus_") {
				kind = "chorus"
			}
			return head + "fx ins " + kind + " {}\ntrack t drums { insert = ins }\npattern p drums steps=1 { bd: x }\nscene s { t = p\n t." + c.Name + " = " + literal + " }\nsong { s }\n"
		}
		return head + "track t drums { " + setting + " }\n" + drums
	case controls.KindBus:
		insert := ""
		if c.Name == "insert" && literal != "none" {
			insert = "fx squash comp {}\n"
		}
		return head + insert + "bus music { " + setting + " }\ntrack t drums {}\n" + drums
	case controls.KindMaster:
		insert := ""
		if c.Name == "insert" && literal != "none" {
			insert = "fx ceiling limiter {}\n"
		}
		return head + insert + "master { " + setting + " }\ntrack t drums {}\n" + drums
	case controls.KindGlobal:
		if c.Name == "tempo" {
			return "cicada 2\ntempo " + literal + "\ntrack t acid {}\npattern p acid steps=1 { 1 }\nscene s { t = p }\nsong { s }\n"
		}
		return head + "track t acid {}\npattern p acid steps=1 transpose=" + literal + " { 1 }\nscene s { t = p }\nsong { s }\n"
	case controls.KindVoice:
		switch owner.Name {
		case "acid":
			return head + "track t acid { " + setting + " }\npattern p acid steps=1 { 1 }\nscene s { t = p }\nsong { s }\n"
		case "drums":
			return head + "track t drums { " + setting + " }\n" + drums
		case "guitar":
			if c.Name == "experimental" {
				return head + "track t guitar { " + setting + " }\n" + notes
			}
			return head + "track t guitar { experimental = on\n " + setting + " }\n" + notes
		}
		return head + "track t " + owner.Name + " { " + setting + " }\n" + notes
	case controls.KindPatch:
		return head + "track t " + owner.Name + " { " + setting + " }\n" + notes
	case controls.KindLibraryPatch:
		patch, _ := instrument.FindPatch(owner.Name)
		source, _ := patch.Source("pad")
		return head + source + "track t pad { " + setting + " }\n" + notes
	case controls.KindAuthored:
		patch, _ := instrument.FindPatch("round-bass")
		source, _ := patch.Source("pad")
		return head + source + "track t pad { " + setting + " }\n" + notes
	case controls.KindKitLane:
		return head + "kit k {\n  bd = model.kick\n}\ntrack t k { bd_" + setting + " }\n" + drums
	case controls.KindSampler:
		fields := map[string]string{"root": "c3", "voices": "4", "mode": "oneshot"}
		var binding string
		if _, base := fields[c.Name]; base {
			fields[c.Name] = literal
			binding = "asset a \"a.wav\" { sha256 = \"" + fakeHash + "\" format = wav frames = 32 rate = 48000Hz channels = 1 }\nsampler pad { asset = a"
			setting = ""
		} else {
			binding = "sampler pad { pack = \"pack.json\" sha256 = \"" + fakeHash + "\""
			delete(fields, "mode")
		}
		for _, name := range []string{"root", "voices", "mode"} {
			if value, ok := fields[name]; ok {
				binding += " " + name + " = " + value
			}
		}
		return head + binding + " " + setting + " }\ntrack t pad {}\n" + notes
	case controls.KindEffect:
		decl := "fx e " + owner.Name + " { " + setting + " }\n"
		if c.Name == "sidechain" {
			return head + decl + "bus music { insert = e }\ntrack t drums {}\n" + drums
		}
		switch owner.Name {
		case "comp", "eq", "deesser", "ampcab", "chorus", "drive":
			return head + decl + "track t drums { insert = e }\n" + drums
		case "transient", "width", "limiter":
			return head + decl + "master { insert = e }\ntrack t drums {}\n" + drums
		case "convolution":
			return head + "asset ir \"ir.wav\" { sha256 = \"" + fakeHash + "\" format = wav frames = 32 rate = 48000Hz channels = 1 }\nfx e convolution { asset = ir " + setting + " }\nmaster { insert = e }\ntrack t drums {}\n" + drums
		}
		return head + decl + "track t drums {}\n" + drums
	}
	return ""
}

// literal writes a number the way the control's declaration takes it.
func literal(c controls.Control, v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + controls.LiteralSuffix(c)
}

// build parses and compiles a score and returns its error diagnostics.
func build(source string) []string {
	score, diagnostics := notation.Parse([]byte(source))
	var errs []string
	for _, d := range diagnostics {
		if d.Severity == "error" {
			errs = append(errs, d.Code+": "+d.Message)
		}
	}
	if len(errs) > 0 || score == nil {
		return append(errs, "no score")
	}
	p, diagnostics := project.FromScore(score)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			errs = append(errs, d.Code+": "+d.Message)
		}
	}
	if p == nil && len(errs) == 0 {
		errs = append(errs, "no project")
	}
	return errs
}

// crossControl reports refusals that come from a rule between controls, such
// as an FM patch needing one audible carrier, rather than from the value's
// own range. The catalog states each such rule in the control's meaning.
func crossControl(errs []string) bool {
	for _, e := range errs {
		for _, rule := range []string{"need an audible carrier", "need a saw or pulse", "needs an octave rank"} {
			if strings.Contains(e, rule) {
				return true
			}
		}
	}
	return false
}

// Every control the catalog lists is accepted by the language at its default,
// at both ends of its range and at every word it lists, and a value just
// outside the range is refused.
func TestCatalogControlsAreAcceptedByTheLanguage(t *testing.T) {
	catalog := controls.All()
	checked := 0
	for _, c := range catalog.Controls {
		owner, _ := catalog.Owner(c.Owner)
		if c.Refused {
			if errs := build(placement(c, owner, controls.Example(c))); len(errs) == 0 {
				t.Errorf("%s is marked refused, but the language accepts %s", c.ID, controls.Example(c))
			}
			continue
		}
		accept := func(what, value string) {
			t.Helper()
			source := placement(c, owner, value)
			if errs := build(source); len(errs) > 0 && !crossControl(errs) {
				t.Errorf("%s %s = %s is refused: %v\n%s", c.ID, what, value, errs, source)
			}
			checked++
		}
		refuse := func(what, value string) {
			t.Helper()
			if errs := build(placement(c, owner, value)); len(errs) == 0 {
				t.Errorf("%s %s = %s is accepted, but the catalog says it is outside the range", c.ID, what, value)
			}
			checked++
		}
		if c.Default != nil || c.Type == "name" {
			accept("default", controls.Example(c))
		}
		for _, word := range c.Values {
			if c.Type == "number" || c.Type == "integer" {
				if _, err := strconv.ParseFloat(word, 64); err == nil {
					accept("word", word+controls.LiteralSuffix(c))
					continue
				}
			}
			accept("word", word)
		}
		if c.Type != "number" && c.Type != "integer" || c.Min == nil || c.Max == nil {
			continue
		}
		accept("min", literal(c, *c.Min))
		accept("max", literal(c, *c.Max))
		for _, v := range c.Excluded {
			refuse("excluded", literal(c, v))
		}
		span := *c.Max - *c.Min
		delta := math.Max(1e-6, span*1e-4)
		if c.Type == "integer" {
			delta = 1
		}
		if c.Owner == "global" && c.Name == "tempo" {
			delta = 1 // the tempo is read in thousandths of a BPM
		}
		refuse("below", literal(c, *c.Min-delta))
		refuse("above", literal(c, *c.Max+delta))
	}
	if checked < 2000 {
		t.Fatalf("only %d placements ran; the catalog walk lost controls", checked)
	}
	t.Logf("%d placements checked", checked)
}

// candidates are the names a validator might accept: every control name the
// catalog knows and the names validators spell in their source.
func candidates(t *testing.T) []string {
	seen := map[string]bool{}
	for _, c := range controls.All().Controls {
		seen[c.Name] = true
		for _, alias := range c.Aliases {
			seen[alias] = true
		}
	}
	for _, name := range validatorLiterals(t) {
		seen[name] = true
	}
	// The keyboard compiler reads its names from the patch table, which
	// builds them with Sprintf, so the table itself is the candidate list.
	for _, patch := range keyboard.Names {
		for _, p := range keyboard.Parameters(patch) {
			seen[p.Name] = true
		}
	}
	var out []string
	for name := range seen {
		if notationIdentifier(name) {
			out = append(out, name)
		}
	}
	return out
}

func notationIdentifier(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// Every name a track block accepts for a voice or patch is a catalog control
// (or a mixer control every track takes). The block is written once with
// every candidate name, and notation names each unknown one.
func TestEveryTrackSettingTheLanguageAcceptsIsInTheCatalog(t *testing.T) {
	catalog := controls.All()
	names := candidates(t)
	known := map[string]map[string]bool{}
	for _, c := range catalog.Controls {
		if known[c.Owner] == nil {
			known[c.Owner] = map[string]bool{}
		}
		name := c.Name
		if c.Owner == "kit_lane" {
			name = "bd_" + name
		}
		known[c.Owner][name] = true
		for _, alias := range c.Aliases {
			known[c.Owner][alias] = true
		}
	}
	mixer := known["mix"]
	mixer["send"] = true // send <return> = <level> is the declaration of send.delay and send.reverb
	checked := 0
	for _, owner := range catalog.Owners {
		switch owner.Kind {
		case controls.KindVoice, controls.KindPatch, controls.KindKitLane, controls.KindLibraryPatch:
		default:
			continue
		}
		tried := names
		if owner.Kind == controls.KindKitLane {
			tried = nil
			for _, name := range names {
				tried = append(tried, "bd_"+name)
			}
		}
		// The probe control's name is the marker: placement writes it as
		// probe_marker = 1, or bd_probe_marker = 1 in a kit lane.
		source := placement(controls.Control{Name: "probe_marker", Owner: owner.Name}, owner, "1")
		marker := "probe_marker = 1"
		if owner.Kind == controls.KindKitLane {
			marker = "bd_probe_marker = 1"
		}
		unknown := map[string]bool{}
		probeNames(t, source, marker, tried, unknown)
		for _, name := range tried {
			checked++
			if unknown[name] {
				continue
			}
			if !known[owner.Name][name] && !mixer[name] && !(owner.Kind == controls.KindLibraryPatch && known["authored"][name]) {
				t.Errorf("a %s track accepts %s, which the catalog does not list", owner.Name, name)
			}
		}
	}
	t.Logf("%d track names probed", checked)
}

// Every setting an effect declaration accepts is a catalog control, and every
// catalog control of an effect is one its compiler names. The accepted names
// are the case labels of each compiler's switch, read from its source.
func TestEffectCompilerSettingsMatchTheCatalog(t *testing.T) {
	catalog := controls.All()
	labels, err := effectCompilerLabels("../../project")
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range catalog.Owners {
		if owner.Kind != controls.KindEffect {
			continue
		}
		accepted := labels[owner.Name]
		if len(accepted) == 0 {
			t.Errorf("no compiler case labels found for effect %s", owner.Name)
			continue
		}
		listed := map[string]bool{}
		for _, c := range catalog.Of(owner.Name) {
			listed[c.Name] = true
			if !accepted[c.Name] {
				t.Errorf("the catalog lists %s, but its compiler accepts no setting %s", c.ID, c.Name)
			}
		}
		for _, binding := range owner.Bindings {
			listed[binding] = true
		}
		for name := range accepted {
			if !listed[name] {
				t.Errorf("effect %s accepts %s, which the catalog does not list", owner.Name, name)
			}
		}
	}
}

func TestPlacementCoversEveryOwner(t *testing.T) {
	for _, owner := range controls.All().Owners {
		probe := controls.Control{Name: "x", Owner: owner.Name}
		if placement(probe, owner, "1") == "" {
			t.Errorf("no placement for %s (%s)", owner.Name, owner.Kind)
		}
	}
	_ = fmt.Sprint()
}

// probeNames writes names into the block at marker and records every name
// notation reports as an unknown parameter. A name the grammar cannot take at
// all (a keyword) is split out by bisection and counts as refused.
func probeNames(t *testing.T, source, marker string, names []string, unknown map[string]bool) {
	t.Helper()
	if len(names) == 0 {
		return
	}
	var lines []string
	for _, name := range names {
		lines = append(lines, name+" = 1")
	}
	score, diagnostics := notation.Parse([]byte(strings.Replace(source, marker, strings.Join(lines, "\n  "), 1)))
	syntax := score == nil
	for _, d := range diagnostics {
		syntax = syntax || d.Code == "CICADA-SYNTAX"
	}
	if syntax {
		if len(names) == 1 {
			unknown[names[0]] = true
			return
		}
		half := len(names) / 2
		probeNames(t, source, marker, names[:half], unknown)
		probeNames(t, source, marker, names[half:], unknown)
		return
	}
	for _, d := range diagnostics {
		if name, ok := strings.CutPrefix(d.Message, "unknown parameter "); ok && d.Code == "CICADA-PARAM" {
			unknown[name] = true
		}
	}
}
