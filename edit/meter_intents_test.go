package edit

import (
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

// waltzArrangement is a 3/4 song: a bar is 2,880 ticks and has beats 1 to 3.
const waltzArrangement = `cicada 2
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

// Every intent that takes or writes an @bar.beat.step position reads and
// writes it on the song's meter map. In 3/4, @2.3.1 is tick 4,800 and @4.1.1
// is tick 8,640; a 4/4 reading would put them at 5,760 and 11,520, and @2.4.1
// does not exist.
func TestPositionIntentsUseTheSongMeter(t *testing.T) {
	for _, c := range []struct {
		intent string
		want   string // text the result must contain
	}{
		{`{"kind":"addmarker","name":"drop","settings":{"at":"@2.3.1"}}`, "marker drop { at = @2.3.1 }"},
		{`{"kind":"addplacement","name":"outro","settings":{"at":"@5.1.1","length":"1bars","track":"bass","content":"riff"}}`, "place outro bass riff { at = @5.1.1 length = 1bars }"},
		{`{"kind":"copyplacement","entity":"placement:verse","name":"verse-2","settings":{"at":"@5.1.1"}}`, "at = @5.1.1"},
		{`{"kind":"setmarker","entity":"marker:chorus","settings":{"at":"@2.3.1"}}`, "marker chorus { at = @2.3.1 }"},
		{`{"kind":"setarrangementtempo","at":"@4.1.1","value":"132"}`, "tempo @4.1.1 132"},
		{`{"kind":"removearrangementtempo","at":"@3.1.1"}`, "tempo @1.1.1 120\n  place"},
		{`{"kind":"setautomationpoint","entity":"param:bass.cutoff","at":"@2.3.1","value":"2kHz","shape":"smooth"}`, "@2.3.1 2kHz smooth"},
		{`{"kind":"removeautomationpoint","entity":"param:bass.cutoff","at":"@3.1.1"}`, "@1.1.1 800Hz\n}"},
		{`{"kind":"moveclip","entity":"placement:verse","attick":8640}`, "place verse bass riff { at = @4.1.1 length = 2bars }"},
		{`{"kind":"splitclip","entity":"placement:intro","attick":8640}`, "at = @4.1.1"},
		{`{"kind":"resizeclip","entity":"placement:verse","lengthticks":8640}`, "place verse bass riff { at = @1.1.1 length = 3bars }"},
	} {
		var env Envelope
		if err := json.Unmarshal([]byte(`{"version":1,"intents":[`+c.intent+`]}`), &env); err != nil {
			t.Fatalf("%s: %v", c.intent, err)
		}
		got, err := Apply([]byte(waltzArrangement), env, Options{Compiler: arrangeCompiler{edition: 2}})
		if err != nil {
			t.Errorf("%s: %v", c.intent, err)
			continue
		}
		if !strings.Contains(string(got.Source), c.want) {
			t.Errorf("%s: result lacks %q:\n%s", c.intent, c.want, got.Source)
			continue
		}
		if s, ds := notation.Parse(got.Source); s == nil || hasErrors(ds) || s.MeterMap().BarTicks(0) != 2880 {
			t.Errorf("%s: result does not validate as 3/4: %v", c.intent, ds)
		}
	}
	// A beat outside a 3/4 bar is refused with the bar and the meter named.
	var env Envelope
	if err := json.Unmarshal([]byte(`{"version":1,"intents":[{"kind":"addmarker","name":"drop","settings":{"at":"@2.4.1"}}]}`), &env); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply([]byte(waltzArrangement), env, Options{Compiler: arrangeCompiler{edition: 2}}); err == nil || !strings.Contains(err.Error(), "outside bar 2, which is in 3/4") {
		t.Fatalf("a fourth beat in 3/4 was not refused by name: %v", err)
	}
}

func TestSetProjectSettingsChangesTheMeter(t *testing.T) {
	apply := func(source, meter string) (*Result, error) {
		var env Envelope
		raw := `{"version":1,"intents":[{"kind":"setprojectsettings","title":"Waltz","tempomilli":120000,"root":"a","scale":"minor","meter":` + jsonString(meter) + `}]}`
		if err := json.Unmarshal([]byte(raw), &env); err != nil {
			t.Fatal(err)
		}
		return Apply([]byte(source), env, Options{Compiler: arrangeCompiler{edition: 2}})
	}
	plain := "cicada 2\ntitle \"Waltz\"\ntempo 120\ntrack bass acid {}\npattern riff { c3 . c4 . }\nscene a { bass = riff }\nsong { a*4 }\n"
	got, err := apply(plain, "3/4")
	if err != nil || !strings.Contains(string(got.Source), "\nmeter 3/4\n") {
		t.Fatalf("adding a meter: %v\n%s", err, got.Source)
	}
	again, err := apply(string(got.Source), "7/8 2+2+3")
	if err != nil || !strings.Contains(string(again.Source), "\nmeter 7/8 2+2+3\n") || strings.Contains(string(again.Source), "meter 3/4") {
		t.Fatalf("changing the meter: %v\n%s", err, again.Source)
	}
	if _, err := apply(plain, "3/5"); err == nil || !strings.Contains(err.Error(), "meter must be N/D") {
		t.Fatalf("an invalid meter was accepted: %v", err)
	}
	// Changing to 3/4 under a fourth-beat position is refused, not rewritten.
	fourBeats := strings.Replace(waltzArrangement, "meter 3/4\n", "", 1)
	fourBeats = strings.Replace(fourBeats, "marker chorus { at = @2.1.1 }", "marker chorus { at = @2.4.1 }", 1)
	fourBeats = strings.Replace(fourBeats, "cicada 2\n", "cicada 2\ntitle \"Waltz\"\n", 1)
	if _, err := apply(fourBeats, "3/4"); err == nil {
		t.Fatal("a meter change that strands @2.4.1 was accepted")
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
