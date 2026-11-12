package project

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/notation"
)

// sendScore declares a reverb named hall and a tape echo named echo, as
// banyan's score does, and adds body after the song.
func sendScore(body string) string {
	return "cicada 2\ntempo 120\nseed 1\nfx hall reverb { decay = 4s }\nfx echo tape { time = 1/8 }\n" +
		"track a acid { send hall = -6dB send echo = 0.2 }\npattern p { c3 . . . }\nscene main { a = p }\nsong { main*4 }\n" + body
}

func sendProject(t *testing.T, source string) (*Project, []notation.Diagnostic) {
	t.Helper()
	score, ds := notation.Parse([]byte(source))
	if hasErrors(ds) {
		return nil, ds
	}
	if ds = notation.Validate(score); hasErrors(ds) {
		return nil, ds
	}
	return FromScore(score)
}

// TestSendAutomationTakesDB is banyan's finding: a track block takes
// send hall = -6dB, but automate a.send.reverb { @1.1.1 -6dB ... } failed
// with "expected unit " and no unit. A send lane's unit is the unit of its
// points. A lane written in dB keeps its points in dB and ramps linearly in dB,
// as level does, with the track block's conversion at every point; off is
// silence, and a segment that starts or ends in it ramps in gain. A lane
// written in linear gains ramps in gain, as before. A lane that mixes the two
// is refused, naming both points.
func TestSendAutomationTakesDB(t *testing.T) {
	p, ds := sendProject(t, sendScore("automate a.send.reverb { @1.1.1 -60dB @2.1.1 0dB @3.1.1 off @4.1.1 -6dB }\n"))
	if hasErrors(ds) {
		t.Fatalf("dB send automation: %v", ds)
	}
	lane := p.Automation[0]
	if lane.Points[0].Value.Unit != "db" || *lane.Points[0].Value.Number != -60 || lane.Points[2].Value.Unit != "enum" || lane.Points[2].Value.Text != "off" {
		t.Fatalf("points %+v", lane.Points)
	}
	// The reviewer's case: with a ramp linear in gain, a -60 dB to 0 dB ramp is
	// at -26 dB after 5% of its length. Linear in dB it is at -57 dB.
	db := func(tick int64) float64 {
		gain, ok := AutomationValue(p, lane, tick)
		if !ok {
			t.Fatal("no value")
		}
		return 20 * math.Log10(gain)
	}
	for _, c := range []struct {
		tick int64
		want float64
	}{{192, -57}, {1920, -30}, {3840, 0}} {
		if got := db(c.tick); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("tick %d: %.6f dB, want %g dB", c.tick, got, c.want)
		}
	}
	// 0 dB to off ramps in gain: halfway is half the gain.
	if gain, _ := AutomationValue(p, lane, 3840+1920); math.Abs(gain-.5) > 1e-12 {
		t.Fatalf("halfway to off: gain %g, want 0.5", gain)
	}
	// Every point is the track block's conversion of its level.
	if gain, _ := AutomationValue(p, lane, 4*3840); gain != paramdefs.SendGainFromDB(-6) {
		t.Fatalf("-6dB point: gain %g, want %g", gain, paramdefs.SendGainFromDB(-6))
	}
	if _, err := CompileAutomation(p); err != nil {
		t.Fatal(err)
	}
	// The track block's send line uses the same conversion.
	if got := p.Tracks[0].Mixer.Sends[0].Level; got.Unit != "db" || *got.Number != -6 {
		t.Fatalf("track send %+v", got)
	}
	// A linear lane holds and ramps linear gain, as before this change.
	linear, ds := sendProject(t, sendScore("automate a.send.reverb { @1.1.1 0 @2.1.1 1 }\n"))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	if gain, _ := AutomationValue(linear, linear.Automation[0], 1920); gain != .5 {
		t.Fatalf("linear lane halfway: %g", gain)
	}
	// One lane, one unit.
	_, ds = sendProject(t, sendScore("automate a.send.reverb { @1.1.1 -6dB @2.1.1 0.5 }\n"))
	if !hasDiagnosticCode(ds, "CICADA-UNIT") || !strings.Contains(ds[0].Message, "@1.1.1 -6dB is in dB and @2.1.1 0.5 is a linear gain") || ds[0].Position.Line != 10 || ds[0].Position.Column != 38 {
		t.Fatalf("mixed units: %v", ds)
	}
	// A scene setting takes dB as a track block does.
	scene, ds := sendProject(t, strings.Replace(sendScore(""), "scene main { a = p }", "scene main { a = p a.send.reverb = -6dB }", 1))
	if hasErrors(ds) {
		t.Fatalf("dB send in a scene: %v", ds)
	}
	if got := scene.Scenes[0].Settings[0].Value; *got.Number != paramdefs.SendGainFromDB(-6) {
		t.Fatalf("scene send %+v", got)
	}
}

// formatGain writes a gain with the fewest digits that parse back to it.
func formatGain(gain float64) string { return strconv.FormatFloat(gain, 'g', -1, 64) }

// TestSendPathTakesTheEffectName checks that a.send.hall, the declared name
// of the reverb, and a.send.echo, the declared name of the tape echo, address
// the same controls as a.send.reverb and a.send.delay, in automation and in
// scenes; that two spellings of one send are one lane too many; and that a
// path naming an effect that is not a send return still gets the hint.
func TestSendPathTakesTheEffectName(t *testing.T) {
	p, ds := sendProject(t, sendScore("automate a.send.hall { @1.1.1 -6dB @2.1.1 0dB }\nautomate a.send.echo { @1.1.1 0.2 @2.1.1 0.5 }\n"))
	if hasErrors(ds) {
		t.Fatalf("send paths by effect name: %v", ds)
	}
	for alias, slot := range map[string]string{"a.send.hall": "a.send.reverb", "a.send.echo": "a.send.delay"} {
		got, err := ResolveParameterPath(p, alias)
		if err != nil {
			t.Fatal(err)
		}
		want, err := ResolveParameterPath(p, slot)
		if err != nil {
			t.Fatal(err)
		}
		if got.Track != want.Track || got.ID != want.ID || got.Descriptor.ID != want.Descriptor.ID {
			t.Fatalf("%s resolves to %+v, %s to %+v", alias, got, slot, want)
		}
	}
	scene, ds := sendProject(t, strings.Replace(sendScore(""), "scene main { a = p }", "scene main { a = p a.send.hall = 0.5 a.send.echo = -12dB }", 1))
	if hasErrors(ds) {
		t.Fatalf("scene send paths by effect name: %v", ds)
	}
	if len(scene.Scenes[0].Settings) != 2 {
		t.Fatalf("scene settings %+v", scene.Scenes[0].Settings)
	}
	_, ds = sendProject(t, sendScore("automate a.send.hall { @1.1.1 0.5 }\nautomate a.send.reverb { @1.1.1 0.25 }\n"))
	if !hasDiagnosticCode(ds, "CICADA-DUPLICATE") {
		t.Fatalf("two spellings of one send: %v", ds)
	}
	_, ds = sendProject(t, "cicada 2\ntempo 120\nfx grit drive {}\ntrack a acid { insert = grit }\npattern p { c3 }\nscene main { a = p }\nsong { main }\nautomate a.send.grit { @1.1.1 0.5 }\n")
	if !hasErrors(ds) || ds[0].Code != "CICADA-PARAM" || !strings.Contains(ds[0].Message, "send.grit names effect grit, a drive, which is not a send return") {
		t.Fatalf("send to a drive: %v", ds)
	}
}

// TestUnitErrorsNameTheUnit is banyan's second complaint: the message read
// "expected unit " with nothing after it. Every numeric registry row now
// names the unit it takes, and shows the value it refused.
func TestUnitErrorsNameTheUnit(t *testing.T) {
	_, ds := sendProject(t, sendScore("automate a.send.reverb { @1.1.1 200Hz }\n"))
	if !hasDiagnosticCode(ds, "CICADA-UNIT") || ds[0].Message != "expected a linear gain from 0 to 1, or a level in dB from -60dB to 0dB; got 200Hz" {
		t.Fatalf("send in Hz: %v", ds)
	}
	_, ds = sendProject(t, sendScore("automate a.send.reverb { @1.1.1 -70dB }\n"))
	if !hasDiagnosticCode(ds, "CICADA-UNIT") || !strings.Contains(ds[0].Message, "-60dB to 0dB") {
		t.Fatalf("send below -60 dB: %v", ds)
	}
	_, ds = sendProject(t, strings.Replace(sendScore(""), "scene main { a = p }", "scene main { a = p a.send.reverb = 200Hz }", 1))
	if !hasDiagnosticCode(ds, "CICADA-UNIT") || !strings.Contains(ds[0].Message, "a linear gain from 0 to 1, or a level in dB from -60dB to 0dB") {
		t.Fatalf("scene send in Hz: %v", ds)
	}
	for _, d := range paramdefs.Registry {
		if d.Curve == "enum" || d.Curve == "toggle" || d.Unit == "enum" {
			continue
		}
		number := d.Default
		wrong := Value{Unit: "ms", Number: &number}
		if strings.EqualFold(d.Unit, "ms") {
			wrong.Unit = "hz"
		}
		err := validateParameterValue(d, wrong)
		if err == nil {
			t.Errorf("%s: a value in %s passed", d.ID, wrong.Unit)
			continue
		}
		message := err.Error()
		if strings.Contains(message, "expected unit ") || !strings.HasPrefix(message, "expected a") || !strings.Contains(message, "; got ") {
			t.Errorf("%s (unit %q): %q does not name the unit it expects", d.ID, d.Unit, message)
		}
	}
}

func hasDiagnosticCode(ds []notation.Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

// TestSendNameIsOneControlEverywhere covers the review of the name alias: two
// spellings of one send are one control for duplicate checks, reported at the
// second spelling; explain's address lookup knows the alias; and a declared
// effect name wins over the return keywords, as in the track block, so with
// fx reverb delay {} the path a.send.reverb is the delay's send.
func TestSendNameIsOneControlEverywhere(t *testing.T) {
	_, ds := sendProject(t, sendScore("automate a.send.hall { @1.1.1 0.5 }\nautomate a.send.reverb { @1.1.1 0.25 }\n"))
	if !hasDiagnosticCode(ds, "CICADA-DUPLICATE") || ds[0].Position.Line != 11 || !strings.Contains(ds[0].Message, "automation paths a.send.hall and a.send.reverb set the same control") {
		t.Fatalf("duplicate lanes: %v", ds)
	}
	p, ds := sendProject(t, sendScore(""))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	address, err := ParamAddressByName(p, "a.send.hall")
	if err != nil || address.Address != "a.send.hall" || address.Param != "mix.send_b" || address.Track != "a" {
		t.Fatalf("address of a.send.hall: %+v %v", address, err)
	}
	crossed := "cicada 2\ntempo 120\nfx reverb delay { time = 1/8 }\nfx hall reverb {}\ntrack a acid { send reverb = 0.4 send hall = 0.2 }\npattern p { c3 . . . }\nscene main { a = p }\nsong { main*2 }\n"
	p, ds = sendProject(t, crossed)
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	for path, want := range map[string]string{"a.send.reverb": "send.delay", "a.send.hall": "send.reverb"} {
		resolved, err := ResolveParameterPath(p, path)
		if err != nil || resolved.Descriptor.Path != want {
			t.Fatalf("%s resolves to %+v, %v; want %s, as the track block send line", path, resolved.Descriptor, err, want)
		}
	}
	_, ds = sendProject(t, crossed+"automate a.send.reverb { @1.1.1 0.5 }\nautomate a.send.delay { @1.1.1 0.25 }\n")
	if !hasDiagnosticCode(ds, "CICADA-DUPLICATE") || !strings.Contains(ds[0].Message, "send.reverb is the send to effect reverb, a delay: a declared effect name wins over the return keyword reverb") {
		t.Fatalf("crossed duplicate: %v", ds)
	}
}
