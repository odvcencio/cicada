package project

import (
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
		_, err = CompilePianoSustain(notation.Track{Kind: "piano", Params: []notation.Param{param}})
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
func toleranceProbes(d paramdefs.Descriptor) []probe {
	tol := paramdefs.Tolerance(d)
	if tol == 0 || d.Type == "integer" {
		return nil // an integer row also rejects any fraction, so slack cannot show
	}
	low, high := float64(float32(d.Min)), float64(float32(d.Max))
	// Below the minimum the keyboard converts to float32 and then compares
	// exactly, so slack passes only where the conversion rounds up onto the limit.
	lowHalf := low - tol/2
	return []probe{
		{"high+tol/2", high + tol/2, true}, {"low-tol/2", lowHalf, float64(float32(lowHalf)) >= low},
		{"high+2tol", high + 2*tol, false}, {"low-2tol", low - 2*tol, false},
	}
}

type agreementCase struct {
	d     paramdefs.Descriptor
	route string
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
		case parts[0] == "acid", parts[0] == "drum", parts[0] == "guitar", parts[0] == "piano" && parts[1] == "sustain", parts[0] == "keys":
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
		span := d.Max - d.Min
		probes := []probe{{"min", d.Min, true}, {"max", d.Max, true}, {"below", d.Min - span*1e-3, false}, {"above", d.Max + span*1e-3, false}}
		for _, v := range d.Isolated {
			probes = append(probes, probe{"isolated", v, true}, probe{"gap", (v + d.Min) / 2, false})
		}
		probes = append(probes, toleranceProbes(d)...)
		for _, p := range probes {
			literal := boundLiteral(d, p.value)
			key := d.ID + "/" + c.route + "/" + p.side
			if got := kernelAccepts(t, d, literal, c.route); got != p.accepts {
				if _, ok := intentionalDifferences[key]; !ok {
					t.Errorf("%s route %s = %s: kernel accepts %v, table says %v", d.ID, c.route, literal, got, p.accepts)
				}
				continue
			}
			if host := paramdefs.RangeMessage(d, "x", literal) == ""; host != p.accepts {
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
		return voiceScore(d, literal, mode)
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

func voiceScore(d paramdefs.Descriptor, literal, mode string) ([]byte, bool) {
	parts := strings.Split(d.ID, ".")
	if mode != "source" && !(d.Live && d.Automatable) {
		return nil, false
	}
	var head, pat, name string
	prefix := "cicada 1\ntempo 120\n"
	switch parts[0] {
	case "acid":
		head, pat, name = "track b acid { %s }\nautomate_here", "pattern riff acid steps=1 { 1 }\nscene main { b = riff SCENE }", "b"
	case "drum":
		head, pat, name = "track b drums { %s }\nautomate_here", "pattern riff drums steps=1 { bd: x }\nscene main { b = riff SCENE }", "b"
	case "keys":
		head, pat, name = "track b "+sceneRegistryKeysPatch(d)+" { %s }\nautomate_here", "pattern riff notes { c4 . }\nscene main { b = riff SCENE }", "b"
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

// The exact limits, written as the shortest literal that round-trips the
// float64, and values just inside each limit, must pass through the score on
// every route and mode the parameter can take. Values just outside, and values
// in the gap before an isolated value, must be reported. No printed value may
// equal the bound it is said to break.
func TestParameterTableExactBoundsOnEveryRouteAndMode(t *testing.T) {
	checked := 0
	for _, c := range agreementCases() {
		d := c.d
		span := d.Max - d.Min
		probes := []probe{{"min", d.Min, true}, {"max", d.Max, true}, {"inside low", d.Min + span*1e-4, true}, {"inside high", d.Max - span*1e-4, true}, {"below", d.Min - span*1e-3, false}, {"above", d.Max + span*1e-3, false}}
		for _, v := range d.Isolated {
			probes = append(probes, probe{"isolated", v, true}, probe{"gap", (v + d.Min) / 2, false})
		}
		probes = append(probes, toleranceProbes(d)...)
		for _, mode := range []string{"source", "automation", "scene"} {
			for _, p := range probes {
				literal := boundLiteral(d, p.value)
				source, ok := scoreOnRoute(d, literal, c.route, mode)
				if !ok {
					continue
				}
				got := rangeWordErrors(t, source)
				if p.accepts == (len(got) > 0) {
					t.Errorf("%s route %s %s %s = %s: range errors %v", d.ID, c.route, mode, p.side, literal, got)
				}
				for _, m := range got {
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
	for _, value := range []string{"2.00000001", "2.00000005", "2"} {
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
