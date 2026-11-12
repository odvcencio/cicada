package edit_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// waltzArrangement is a 3/4 song: a bar is 2,880 ticks and has beats 1 to 3.
const waltzArrangement = `cicada 2
title "Waltz"
tempo 120
meter 3/4
track bass acid {}
pattern riff { c3 . c4 . }
automate bass.cutoff {
  @1.1.1 800Hz
  @3.1.1 1200Hz
}
arrange {
  tempo @1.1.1 120
  tempo @3.1.1 100
  place verse bass riff { at = @1.1.1 length = 2bars }
  place intro bass riff { at = @3.1.1 length = 2bars }
  marker chorus { at = @2.1.1 }
}
`

// meterCompiler compiles one source the way Studio and the command line do.
type meterCompiler struct{}

func (meterCompiler) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	score, ds := notation.ParseEdition(source, 2)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	p, ds := project.FromScore(score)
	for _, d := range ds {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s", d.Message)
		}
	}
	return project.EditPlan(p, source), nil
}

// sameMusic reports every source as the same render, so an intent that must
// prove it keeps the music (splitclip) can run here; these tests check
// positions, not audio.
type sameMusic struct{}

func (sameMusic) Hash([]byte) (string, error) { return "same", nil }

func applyMeterIntent(t *testing.T, source, intent string) (*edits.Result, error) {
	t.Helper()
	var env edits.Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+intent+`]}`), &env); err != nil {
		t.Fatalf("%s: %v", intent, err)
	}
	return edits.Apply([]byte(source), env, edits.Options{Compiler: meterCompiler{}, Edition: 2, RenderCheck: sameMusic{}})
}

// Every intent that takes or writes an @bar.beat.step position reads and
// writes it on the song's meter map. In 3/4, @2.3.1 is tick 4,800 and @5.1.1
// is tick 11,520; a 4/4 reading would put them at 5,760 and 15,360, and @2.4.1
// does not exist.
func TestPositionIntentsUseTheSongMeter(t *testing.T) {
	for _, c := range []struct {
		intent       string
		want, absent string
	}{
		{`{"kind":"addmarker","name":"drop","settings":{"at":"@2.3.1"}}`, "marker drop { at = @2.3.1 }", ""},
		{`{"kind":"addplacement","name":"outro","settings":{"at":"@5.1.1","length":"1bars","track":"bass","content":"riff"}}`, "place outro bass riff { at = @5.1.1 length = 1bars }", ""},
		{`{"kind":"copyplacement","entity":"placement:verse","name":"verse-2","settings":{"at":"@5.1.1"}}`, "at = @5.1.1", ""},
		{`{"kind":"setmarker","entity":"marker:chorus","settings":{"at":"@2.3.1"}}`, "marker chorus { at = @2.3.1 }", ""},
		{`{"kind":"setarrangementtempo","at":"@4.1.1","value":"132"}`, "tempo @4.1.1 132", ""},
		{`{"kind":"removearrangementtempo","at":"@3.1.1"}`, "tempo @1.1.1 120", "tempo @3.1.1"},
		{`{"kind":"setautomationpoint","entity":"param:bass.cutoff","at":"@2.3.1","value":"2kHz","shape":"smooth"}`, "@2.3.1 2000Hz smooth", ""},
		{`{"kind":"removeautomationpoint","entity":"param:bass.cutoff","at":"@3.1.1"}`, "@1.1.1 800Hz", "1200Hz"},
		{`{"kind":"moveclip","entity":"placement:intro","attick":11520}`, "place intro bass riff { at = @5.1.1 length = 2bars }", ""},
		{`{"kind":"splitclip","entity":"placement:intro","attick":8640}`, "at = @4.1.1", ""},
		{`{"kind":"updateplacement","entity":"placement:intro","settings":{"at":"@4.2.1"}}`, "place intro bass riff { at = @4.2.1", ""},
		{`{"kind":"duplicateclip","entity":"placement:intro"}`, "at = @5.1.1", ""},
		{`{"kind":"resizeclip","entity":"placement:verse","lengthticks":2880}`, "place verse bass riff { at = @1.1.1 length = 1bar", "length = 2bars }\n  place intro"},
	} {
		got, err := applyMeterIntent(t, waltzArrangement, c.intent)
		if err != nil {
			t.Errorf("%s: %v", c.intent, err)
			continue
		}
		source := string(got.Source)
		if !strings.Contains(source, c.want) || c.absent != "" && strings.Contains(source, c.absent) {
			t.Errorf("%s: result should contain %q and not %q:\n%s", c.intent, c.want, c.absent, source)
			continue
		}
		if s, ds := notation.Parse(got.Source); s == nil || s.MeterMap().BarTicks(0) != 2880 {
			t.Errorf("%s: result is not a 3/4 score: %v", c.intent, ds)
		}
	}
	// A fourth beat in a 3/4 bar is refused with the bar and the meter named.
	if _, err := applyMeterIntent(t, waltzArrangement, `{"kind":"addmarker","name":"drop","settings":{"at":"@2.4.1"}}`); err == nil || !strings.Contains(err.Error(), "outside bar 2, which is in 3/4") {
		t.Fatalf("a fourth beat in 3/4 was not refused by name: %v", err)
	}
}

func TestSetProjectSettingsChangesTheMeter(t *testing.T) {
	settings := func(meter string) string {
		m, _ := json.Marshal(meter)
		return `{"kind":"setprojectsettings","title":"Waltz","tempomilli":120000,"root":"a","scale":"minor","meter":` + string(m) + `}`
	}
	plain := "cicada 2\ntitle \"Waltz\"\ntempo 120\ntrack bass acid {}\npattern riff { c3 . c4 . }\nscene a { bass = riff }\nsong { a*4 }\n"
	got, err := applyMeterIntent(t, plain, settings("3/4"))
	if err != nil || !strings.Contains(string(got.Source), "\nmeter 3/4\n") {
		t.Fatalf("adding a meter: %v\n%v", err, got)
	}
	again, err := applyMeterIntent(t, string(got.Source), settings("7/8 2+2+3"))
	if err != nil || !strings.Contains(string(again.Source), "\nmeter 7/8 2+2+3\n") || strings.Contains(string(again.Source), "meter 3/4") {
		t.Fatalf("changing the meter: %v\n%v", err, again)
	}
	if _, err := applyMeterIntent(t, plain, settings("3/5")); err == nil || !strings.Contains(err.Error(), "meter must be N/D") {
		t.Fatalf("an invalid meter was accepted: %v", err)
	}
	// Changing to 3/4 under a fourth-beat position is refused, not rewritten.
	fourBeats := strings.Replace(waltzArrangement, "meter 3/4\n", "", 1)
	fourBeats = strings.Replace(fourBeats, "marker chorus { at = @2.1.1 }", "marker chorus { at = @2.4.1 }", 1)
	if _, err := applyMeterIntent(t, fourBeats, settings("3/4")); err == nil {
		t.Fatal("a meter change that strands @2.4.1 was accepted")
	}
}
