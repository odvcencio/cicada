package project

import (
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/notation"
)

// A route is a way a parameter reaches the kernel. A device can take more than
// one: an EQ or compressor can sit on a track, on the music bus, or on the
// master chain, and the limits can differ between routes.
const (
	routeTrack  = "track"  // voice setting, or an effect inserted on a track
	routeMaster = "master" // effect in the master chain (built by pro.New at 48 kHz)
	routeBus    = "bus"    // compressor inserted on the music bus
	routeFree   = "free"   // declared effect with no insert, as scenes and automation address it
	// A modeled keyboard track: every setting it accepts, the rows inherited
	// from the piano included, is compiled by KeysSpecFromValues.
	routeKeyboard = "keyboard"
)

// kernelAccepts sends one literal through the project compilers, which end in
// the kernel Validate functions, and reports whether the kernel took it.
func kernelAccepts(t *testing.T, d paramdefs.Descriptor, literal, route string) bool {
	t.Helper()
	param := notation.Param{Name: d.Source, Value: literal}
	value, err := projectValue(literal)
	if err != nil {
		t.Fatalf("%s: %v", d.ID, err)
	}
	values := map[string]Value{d.Source: value}
	parts := strings.Split(d.ID, ".")
	switch parts[0] {
	case "acid":
		_, err = CompileAcidParams(notation.Track{Kind: "acid", Params: []notation.Param{param}})
	case "drum":
		_, err = CompileDrumParams(notation.Track{Kind: "drums", Params: []notation.Param{param}})
	case "guitar":
		_, err = CompileGuitarParams(notation.Track{Kind: "guitar", Params: []notation.Param{{Name: "experimental", Value: "on"}, param}})
	case "piano":
		if route == routeKeyboard {
			_, err = KeysSpecFromValues("tine_ep", values)
		} else {
			_, err = CompilePianoSustain(notation.Track{Kind: "piano", Params: []notation.Param{param}})
		}
	case "keys":
		_, err = KeysSpecFromValues(sceneRegistryKeysPatch(d), values)
	case "fx":
		kind := parts[1]
		if kind == "master" {
			kind = parts[2]
		}
		if route == routeMaster {
			err = masterRouteError(kind, values)
			break
		}
		switch kind {
		case "drive":
			_, err = DriveParamsFromValues(values)
		case "delay":
			_, err = DelayParamsFromValues(values)
		case "reverb":
			_, err = ReverbParamsFromValues(values)
		case "comp":
			_, _, err = CompSpecFromValues(values)
		case "deesser":
			_, err = DeesserParamsFromValues(values)
		case "eq":
			_, err = compileTrackEffect(Effect{ID: "e", Kind: "eq", Params: values}, 48000)
		default:
			t.Fatalf("no non-master route for %s", d.ID)
		}
	default:
		t.Fatalf("no kernel path for %s", d.ID)
	}
	return err == nil
}

// masterRouteError is what validateMasterEffect does for a master insert.
func masterRouteError(kind string, values map[string]Value) error {
	params, err := masterEffectParams(Effect{ID: "e", Kind: kind, Params: values})
	if err == nil {
		_, err = pro.New(48000, params)
	}
	return err
}

// intentionalDifferences lists rows where the kernel and the host table
// disagree on purpose, with the reason. No row may differ without an entry.
// Keys read "<row id>/<route>/<min|max|below|above|isolated>". It is empty.
var intentionalDifferences = map[string]string{}

// routesFor lists the routes an effect kind can take, and the row to use on
// each: the master route swaps in the master-specific row where one exists.
func routesFor(kind string) []string {
	switch kind {
	case "transient", "width", "limiter":
		return []string{routeMaster}
	case "eq":
		return []string{routeTrack, routeMaster}
	case "comp":
		return []string{routeTrack, routeMaster, routeBus}
	case "drive", "deesser":
		return []string{routeTrack}
	}
	return []string{routeFree} // delay, reverb
}

type probe struct {
	side    string
	value   float64
	accepts bool
}

// toleranceProbes test the owning validator's own slack: half the tolerance
// beyond each limit must pass, and twice the tolerance beyond must fail. The
// limits are the validator's, so keyboard limits are taken after float32
// conversion. After the float32 conversion the keyboard still compares exactly,
// so only values that round onto a limit pass.
func toleranceProbes(d paramdefs.Descriptor, route, mode string) []probe {
	tol := keysTolerance
	if route != routeKeyboard || d.Type == "integer" {
		return nil // an integer row also rejects any fraction, so slack cannot show
	}
	low, high := float64(float32(d.Min)), float64(float32(d.Max))
	// Below the minimum the keyboard converts to float32 and then compares
	// exactly, so slack passes only where the conversion rounds up onto the limit.
	lowHalf := low - tol/2
	probes := []probe{
		{"high+tol/2", high + tol/2, true}, {"low-tol/2", lowHalf, float64(float32(lowHalf)) >= low},
		{"high+2tol", high + 2*tol, false}, {"low-2tol", low - 2*tol, false},
	}
	if mode != "" && mode != "source" {
		// Scene settings and automation points are validated by the exact
		// descriptor comparison (validateParameterValue and the registry
		// range), not by the keyboard compiler, so no slack applies.
		for i := range probes {
			probes[i].accepts = false
		}
	}
	return probes
}

type agreementCase struct {
	d     paramdefs.Descriptor
	route string
}

// probesFor lists the values to try for a case in a mode ("" is the compiler).
func probesFor(c agreementCase, mode string) []probe {
	d := c.d
	span := d.Max - d.Min
	inLow, inHigh := d.Min+span*1e-4, d.Max-span*1e-4
	// A fraction is a different error. The registry does not mark acid.gate
	// integer, but pattern compilation reads the track gate with Atoi
	// (project/compile.go), so a fractional gate is rejected there with its own
	// message; the table is left alone because Type feeds the kernel image.
	if d.Type == "integer" || d.ID == "acid.gate" {
		inLow, inHigh = d.Min+1, d.Max-1
	}
	probes := []probe{{"min", d.Min, true}, {"max", d.Max, true}, {"inside low", inLow, true}, {"inside high", inHigh, true}, {"below", d.Min - span*1e-3, false}, {"above", d.Max + span*1e-3, false}}
	for _, v := range d.Isolated {
		probes = append(probes, probe{"isolated", v, true}, probe{"gap", (v + d.Min) / 2, false})
	}
	return append(probes, toleranceProbes(d, c.route, mode)...)
}

func agreementCases() []agreementCase {
	var cases []agreementCase
	rows := append(append([]paramdefs.Descriptor(nil), paramdefs.Registry...), paramdefs.HostOnly...)
	for _, d := range rows {
		parts := strings.Split(d.ID, ".")
		switch {
		case strings.HasPrefix(d.ID, "mix."), d.Source == "octave", d.Source == "sidechain", d.Curve == "enum", d.Curve == "toggle":
			continue
		case parts[0] == "fx" && parts[1] == "master":
			continue // reached through routesFor("eq")
		case parts[0] == "fx":
			for _, route := range routesFor(parts[1]) {
				row := d
				if route == routeMaster {
					row, _ = paramdefs.DeviceParam(parts[1], d.Source, true)
				}
				cases = append(cases, agreementCase{row, route})
			}
		case parts[0] == "piano" && parts[1] == "sustain":
			cases = append(cases, agreementCase{d, routeTrack}, agreementCase{d, routeKeyboard})
		case parts[0] == "keys":
			cases = append(cases, agreementCase{d, routeKeyboard})
		case parts[0] == "acid", parts[0] == "drum", parts[0] == "guitar":
			cases = append(cases, agreementCase{d, routeTrack})
		}
	}
	return cases
}

// The host table and the kernel must agree on every numeric parameter, on every
// route: the limits pass the kernel, a value just outside each limit fails it,
// and any isolated value outside the interval (such as 0 to bypass) passes
// while a value between it and the interval fails.
func TestParameterTableAgreesWithKernelValidation(t *testing.T) {
	checked := 0
	for _, c := range agreementCases() {
		d := c.d
		probes := probesFor(c, "")
		for _, p := range probes {
			literal := boundLiteral(d, p.value)
			key := d.ID + "/" + c.route + "/" + p.side
			if got := kernelAccepts(t, d, literal, c.route); got != p.accepts {
				if _, ok := intentionalDifferences[key]; !ok {
					t.Errorf("%s route %s = %s: kernel accepts %v, table says %v", d.ID, c.route, literal, got, p.accepts)
				}
				continue
			}
			message := hostMessage(c, literal)
			if host := message == ""; host != p.accepts {
				t.Errorf("%s route %s = %s: host table accepts %v, want %v", d.ID, c.route, literal, host, p.accepts)
			}
			checked++
		}
	}
	if checked < 400 {
		t.Fatalf("only %d limit checks ran; the table walk lost rows", checked)
	}
}

// scoreOnRoute places one setting where its route needs it. mode is "source"
// (the setting in the declaration), "automation" or "scene". It reports false
// where the route does not allow the mode.
func scoreOnRoute(d paramdefs.Descriptor, literal, route, mode string) ([]byte, bool) {
	parts := strings.Split(d.ID, ".")
	live := d.Live && d.Automatable
	var track, devices, path, bindings string
	track = "d"
	switch parts[0] {
	case "acid", "drum", "guitar", "piano", "keys":
		return voiceScore(d, literal, route, mode)
	case "fx":
		kind := parts[1]
		if kind == "master" {
			kind = parts[2]
		}
		switch route {
		case routeTrack:
			devices = "fx e " + kind + " { %s }\ntrack d drums { insert = e }"
		case routeMaster:
			devices = "fx e " + kind + " { %s }\nmaster { insert = e }\ntrack d drums {}"
		case routeBus:
			devices = "fx e " + kind + " { %s }\nbus music { insert = e }\ntrack d drums {}"
		default:
			devices = "fx e " + kind + " { %s }\ntrack d drums {}"
		}
		path = "e." + d.Source
		if mode != "source" && (route != routeFree || !live) {
			return nil, false // routed effects are fixed until recompiled
		}
	}
	setting := d.Source + " = " + literal
	switch mode {
	case "source":
		devices = strings.Replace(devices, "%s", setting, 1)
	case "automation":
		devices = strings.Replace(devices, "%s", "", 1) + "\nautomate " + path + " { @1.1.1 " + literal + " }"
	case "scene":
		devices = strings.Replace(devices, "%s", "", 1)
		bindings = path + " = " + literal
	}
	return []byte("cicada 1\ntempo 120\n" + devices + "\npattern beat drums steps=1 { bd: x }\nscene main { " + track + " = beat " + bindings + " }\nsong { main }\n"), true
}

func keysPatchFor(d paramdefs.Descriptor) string {
	if patch := sceneRegistryKeysPatch(d); patch != "" {
		return patch
	}
	return "tine_ep"
}

func voiceScore(d paramdefs.Descriptor, literal, route, mode string) ([]byte, bool) {
	parts := strings.Split(d.ID, ".")
	if mode != "source" && !(d.Live && d.Automatable) {
		return nil, false
	}
	var head, pat, name string
	prefix := "cicada 1\ntempo 120\n"
	kind := parts[0]
	if kind == "piano" && route == routeKeyboard {
		kind = "keys"
	}
	switch kind {
	case "acid":
		head, pat, name = "track b acid { %s }\nautomate_here", "pattern riff acid steps=1 { 1 }\nscene main { b = riff SCENE }", "b"
	case "drum":
		head, pat, name = "track b drums { %s }\nautomate_here", "pattern riff drums steps=1 { bd: x }\nscene main { b = riff SCENE }", "b"
	case "keys":
		head, pat, name = "track b "+keysPatchFor(d)+" { %s }\nautomate_here", "pattern riff notes { c4 . }\nscene main { b = riff SCENE }", "b"
		prefix = "cicada 2\ntempo 120\n"
	default:
		return nil, false // guitar and piano have their own preconditions; the compiler route covers them
	}
	setting := d.Source + " = " + literal
	auto, scene := "", ""
	switch mode {
	case "source":
		head = strings.Replace(head, "%s", setting, 1)
	case "automation":
		head = strings.Replace(head, "%s", "", 1)
		auto = "automate " + name + "." + d.Path + " { @1.1.1 " + literal + " }"
	case "scene":
		head = strings.Replace(head, "%s", "", 1)
		scene = name + "." + d.Path + " = " + literal
	}
	head = strings.Replace(head, "automate_here", auto, 1)
	pat = strings.Replace(pat, "SCENE", scene, 1)
	return []byte(prefix + head + "\n" + pat + "\nsong { main }\n"), true
}

// buildErrors parses and builds the score. ok is false if there is no project.
func buildErrors(t *testing.T, source []byte) (errs []notation.Diagnostic, ok bool) {
	t.Helper()
	score, diagnostics := notation.Parse(source)
	if !hasErrors(diagnostics) {
		var p *Project
		p, more := FromScore(score)
		diagnostics = append(diagnostics, more...)
		ok = p != nil
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			errs = append(errs, d)
		}
	}
	return errs, ok && len(errs) == 0
}

// The exact limits, written as the shortest literal that round-trips the
// float64, and values just inside each limit, must pass through the score on
// every route and mode the parameter can take: no error diagnostic at all and a
// built project. Values just outside, and values in the gap before an isolated
// value, must be rejected (in the source mode, with a range message). No
// printed value may equal the bound it is said to break.
func TestParameterTableExactBoundsOnEveryRouteAndMode(t *testing.T) {
	checked := 0
	for _, c := range agreementCases() {
		d := c.d
		for _, mode := range []string{"source", "automation", "scene"} {
			for _, p := range probesFor(c, mode) {
				literal := boundLiteral(d, p.value)
				source, ok := scoreOnRoute(d, literal, c.route, mode)
				if !ok {
					continue
				}
				errs, built := buildErrors(t, source)
				switch {
				case p.accepts && !built:
					t.Errorf("%s route %s %s %s = %s: rejected: %+v", d.ID, c.route, mode, p.side, literal, errs)
				case !p.accepts && len(errs) == 0:
					t.Errorf("%s route %s %s %s = %s: accepted", d.ID, c.route, mode, p.side, literal)
				}
				for _, e := range errs {
					m := e.Message
					if strings.Contains(m, literal+" is below the minimum "+literal+" ") || strings.Contains(m, literal+" is above the maximum "+literal+" ") {
						t.Errorf("contradictory message %q", m)
					}
				}
				checked++
			}
		}
	}
	if checked < 600 {
		t.Fatalf("only %d source checks ran", checked)
	}
}

// Accepted projects must survive the JSON and source round trips.
func TestMasterEQAndWidthBypassSurviveJSONAndSourceRoundTrips(t *testing.T) {
	for _, tc := range []struct{ name, devices string }{
		{"master eq at its limits", "fx lo eq { frequency = 10Hz }\nfx hi eq { frequency = 21600Hz }\nmaster { insert = lo -> hi }\ntrack d drums {}"},
		{"master eq above the track limit", "fx e eq { frequency = 20000Hz }\nmaster { insert = e }\ntrack d drums {}"},
		{"width bypass 0Hz", "fx w width { bass_mono = 0Hz }\nmaster { insert = w }\ntrack d drums {}"},
		{"width bypass plain 0", "fx w width { bass_mono = 0 }\nmaster { insert = w }\ntrack d drums {}"},
		{"width at its limits", "fx a width { bass_mono = 20Hz }\nfx b width { bass_mono = 500Hz }\nmaster { insert = a -> b }\ntrack d drums {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			score, diagnostics := notation.Parse(rangeScore(tc.devices))
			if hasErrors(diagnostics) {
				t.Fatal(diagnostics)
			}
			p, diagnostics := FromScore(score)
			if p == nil {
				t.Fatalf("FromScore: %+v", diagnostics)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProject(decoded); err != nil {
				t.Fatalf("JSON project: %v", err)
			}
			source, err := ToSource(decoded)
			if err != nil {
				t.Fatal(err)
			}
			again, diagnostics := notation.Parse(source)
			if hasErrors(diagnostics) {
				t.Fatalf("exported source: %+v\n%s", diagnostics, source)
			}
			if q, diagnostics := FromScore(again); q == nil {
				t.Fatalf("exported source: %+v\n%s", diagnostics, source)
			}
		})
	}
}

func TestMasterEQAndWidthRejectValuesOutsideTheirOwnLimits(t *testing.T) {
	for _, tc := range []struct{ devices, want string }{
		{"fx e eq { frequency = 21601Hz }\nmaster { insert = e }\ntrack d drums {}", "eq frequency 21601Hz is above the maximum 21600Hz (range 10Hz to 21600Hz)"},
		{"fx e eq { frequency = 20000Hz }\ntrack d drums { insert = e }", "eq frequency 20000Hz is above the maximum 19845Hz"},
		{"fx w width { bass_mono = 10Hz }\nmaster { insert = w }\ntrack d drums {}", "width bass_mono 10Hz is below the minimum 20Hz (range 20Hz to 500Hz, or 0 to bypass)"},
	} {
		got := rangeWordErrors(t, rangeScore(tc.devices))
		if len(got) != 1 || !strings.HasPrefix(got[0], tc.want) {
			t.Errorf("got %v, want prefix %q", got, tc.want)
		}
	}
}

// project/keys.go accepts a keyboard value up to 1e-7 outside its float32
// limits. The pre-check, ValidateProject and the JSON round trips must accept
// exactly what the compiler accepts.
func TestKeyboardToleranceIsAcceptedEverywhere(t *testing.T) {
	for _, value := range []string{"2.00000001", "2.00000005", "2", "200.00001%", "2.0000001"} {
		t.Run(value, func(t *testing.T) {
			source := []byte("cicada 2\ntempo 120\ntrack e tine_ep { gain = " + value + " }\npattern p notes { c4 . }\nscene main { e = p }\nsong { main }\n")
			score, diagnostics := notation.Parse(source)
			if hasErrors(diagnostics) {
				t.Fatal(diagnostics)
			}
			p, diagnostics := FromScore(score)
			if p == nil {
				t.Fatalf("FromScore: %+v", diagnostics)
			}
			if err := ValidateProject(p); err != nil {
				t.Fatalf("ValidateProject: %v", err)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			exported, err := ToSource(decoded)
			if err != nil {
				t.Fatal(err)
			}
			again, diagnostics := notation.Parse(exported)
			if hasErrors(diagnostics) {
				t.Fatalf("exported source: %+v\n%s", diagnostics, exported)
			}
			if q, diagnostics := FromScore(again); q == nil {
				t.Fatalf("exported source: %+v\n%s", diagnostics, exported)
			}
		})
	}
	source := []byte("cicada 2\ntempo 120\ntrack e tine_ep { gain = 2.0000002 }\npattern p notes { c4 . }\nscene main { e = p }\nsong { main }\n")
	if got := scoreErrors(t, source); len(got) != 1 || !strings.Contains(got[0].Message, "tine_ep gain 2.0000002 is above the maximum 2") {
		t.Fatalf("beyond the tolerance: %+v", got)
	}
	if _, err := KeysSpecFromValues("tine_ep", map[string]Value{"gain": {Unit: "unit", Number: ptr(2.0000002)}}); err == nil {
		t.Fatal("the compiler accepted a value beyond its tolerance")
	}
}

func ptr(v float64) *float64 { return &v }

func rangeWordErrors(t *testing.T, source []byte) []string {
	t.Helper()
	var out []string
	for _, d := range scoreErrors(t, source) {
		if strings.Contains(d.Message, "the minimum") || strings.Contains(d.Message, "the maximum") {
			out = append(out, d.Message)
		}
	}
	return out
}

// A keyboard track also takes the rows it inherits from the piano, such as
// sustain. They are compiled by KeysSpecFromValues, so the keyboard rule
// applies; a real piano track keeps the exact comparison.
func TestInheritedPianoRowsFollowTheKeyboardRuleOnKeyboardTracks(t *testing.T) {
	for _, patch := range []string{"tine_ep", "fm_ep"} {
		t.Run(patch, func(t *testing.T) {
			source := []byte("cicada 2\ntempo 120\ntrack e " + patch + " { sustain = 1.00000005 }\npattern p notes { c4 . }\nscene main { e = p }\nsong { main }\n")
			score, diagnostics := notation.Parse(source)
			if hasErrors(diagnostics) {
				t.Fatal(diagnostics)
			}
			p, diagnostics := FromScore(score)
			if p == nil {
				t.Fatalf("FromScore: %+v", diagnostics)
			}
			if _, err := KeysSpecFromValues(patch, p.Tracks[0].Params); err != nil {
				t.Fatalf("compiler: %v", err)
			}
			if err := ValidateProject(p); err != nil {
				t.Fatalf("ValidateProject: %v", err)
			}
			data, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			exported, err := ToSource(decoded)
			if err != nil {
				t.Fatal(err)
			}
			again, diagnostics := notation.Parse(exported)
			if hasErrors(diagnostics) {
				t.Fatalf("exported source: %+v\n%s", diagnostics, exported)
			}
			if q, diagnostics := FromScore(again); q == nil {
				t.Fatalf("exported source: %+v\n%s", diagnostics, exported)
			}
		})
	}
	piano := []byte("cicada 2\ntempo 120\ntrack p piano { sustain = 1.00000005 }\npattern q notes { c4 . }\nscene main { p = q }\nsong { main }\n")
	if got := scoreErrors(t, piano); len(got) != 1 || got[0].Position.Line != 3 || !strings.Contains(got[0].Message, "piano sustain") {
		t.Fatalf("a real piano must keep the exact comparison: %+v", got)
	}
}

// hostMessage is the pre-check's verdict for a case: the keyboard comparison on
// the keyboard route, the exact one elsewhere.
func hostMessage(c agreementCase, literal string) string {
	if c.route == routeKeyboard {
		return keysRangeMessage(keysPatchFor(c.d), c.d, "x", c.d.Source, literal)
	}
	return paramdefs.RangeMessage(c.d, "x", literal)
}

// shiftText moves the decimal point of a plain decimal text by places digits,
// without arithmetic, so a spelling is exact.
func shiftText(text string, places int) string {
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	point := strings.IndexByte(text, '.')
	if point < 0 {
		point = len(text)
	}
	digits := strings.ReplaceAll(text, ".", "")
	at := point + places
	switch {
	case at <= 0:
		digits = "0." + strings.Repeat("0", -at) + digits
	case at >= len(digits):
		digits += strings.Repeat("0", at-len(digits))
	default:
		digits = digits[:at] + "." + digits[at:]
	}
	if strings.Contains(digits, ".") {
		digits = strings.TrimRight(digits, "0")
		digits = strings.TrimSuffix(digits, ".")
	}
	return sign + digits
}

// spellings writes one base-unit value in every spelling the source accepts
// for the row's unit: the base unit, the scaled unit (kHz, s, %), and
// scientific notation.
func spellings(d paramdefs.Descriptor, value float64) []string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	sci := strconv.FormatFloat(value, 'e', -1, 64)
	switch strings.ToLower(d.Unit) {
	case "hz":
		return []string{text + "Hz", shiftText(text, -3) + "kHz", sci + "Hz", text + "hz"}
	case "ms":
		return []string{text + "ms", shiftText(text, -3) + "s", sci + "ms"}
	case "db":
		return []string{text + "dB", sci + "dB"}
	case "dbtp":
		return []string{text + "dBTP", sci + "dBTP"}
	}
	return []string{text, shiftText(text, 2) + "%", sci}
}

// At every boundary, every accepted spelling of the same value must get the
// same verdict from the pre-check as from the compiler that owns the route.
func TestEverySpellingOfABoundaryAgreesWithTheCompiler(t *testing.T) {
	checked := 0
	for _, c := range agreementCases() {
		for _, p := range probesFor(c, "") {
			for _, literal := range spellings(c.d, p.value) {
				if _, err := projectValue(literal); err != nil {
					continue
				}
				// A spelling the compiler rejects for its form (not its value),
				// such as scientific notation for the whole-number gate, is not
				// a range question: the pre-check stays silent and the compiler
				// reports it.
				kernel := kernelAccepts(t, c.d, literal, c.route)
				host := hostMessage(c, literal) == ""
				if kernel != host && !(host && !kernel && formOnly(c.d, literal)) {
					t.Errorf("%s route %s %s = %s: compiler accepts %v, pre-check accepts %v", c.d.ID, c.route, p.side, literal, kernel, host)
				}
				checked++
			}
		}
	}
	if checked < 1500 {
		t.Fatalf("only %d spellings were compared", checked)
	}
}

// formOnly reports spellings the compiler refuses for a reason other than range.
func formOnly(d paramdefs.Descriptor, literal string) bool {
	return d.ID == "acid.gate" && strings.ContainsAny(literal, "eE.")
}
