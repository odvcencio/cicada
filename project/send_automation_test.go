package project

import (
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
// with "expected unit " and no unit. A send point now takes dB with the
// track block's conversion, and the lane holds and interpolates linear gain,
// so a linear lane compiles exactly as before.
func TestSendAutomationTakesDB(t *testing.T) {
	p, ds := sendProject(t, sendScore("automate a.send.reverb { @1.1.1 -6dB @2.1.1 0dB smooth @3.1.1 0.25 }\n"))
	if hasErrors(ds) {
		t.Fatalf("dB send automation: %v", ds)
	}
	points := p.Automation[0].Points
	want := []float64{paramdefs.SendGainFromDB(-6), 1, 0.25}
	for i, point := range points {
		if point.Value.Unit != "unit" || *point.Value.Number != want[i] {
			t.Fatalf("point %d: %+v, want linear gain %v", i, point.Value, want[i])
		}
	}
	// The track block's send line uses the same conversion.
	if got := p.Tracks[0].Mixer.Sends[0].Level; got.Unit != "db" || *got.Number != -6 || paramdefs.SendGainFromDB(*got.Number) != want[0] {
		t.Fatalf("track send %+v", got)
	}
	// A lane written in dB compiles to the controls of the same lane written
	// with the linear gains: the ramp is linear in gain.
	linear, ds := sendProject(t, sendScore("automate a.send.reverb { @1.1.1 "+formatGain(want[0])+" @2.1.1 1 smooth @3.1.1 0.25 }\n"))
	if hasErrors(ds) {
		t.Fatal(ds)
	}
	a, err := CompileAutomation(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CompileAutomation(linear)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) || len(a) < 3 {
		t.Fatalf("%d controls from dB, %d from linear gain", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("control %d: %+v from dB, %+v from linear gain", i, a[i], b[i])
		}
	}
	// A scene setting takes dB the same way.
	scene, ds := sendProject(t, strings.Replace(sendScore(""), "scene main { a = p }", "scene main { a = p a.send.reverb = -6dB }", 1))
	if hasErrors(ds) {
		t.Fatalf("dB send in a scene: %v", ds)
	}
	if got := scene.Scenes[0].Settings[0].Value; *got.Number != want[0] {
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
	if !hasErrors(ds) || !strings.Contains(ds[0].Message, "only the delay and reverb returns have track send paths") {
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
