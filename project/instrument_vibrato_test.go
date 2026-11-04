package project

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/notation"
)

func TestInstrumentVibratoInheritsAndTrackOverrides(t *testing.T) {
	source := `cicada 2
instrument lead {
  vibrato = 20ct
  vibrato_rate = 6Hz
  vibrato_delay = 150ms
  voice mono { out = sine(pitch) * env(gate, 100ms) }
}
track first lead {}
track second lead { vibrato = 40ct vibrato_rate = 8Hz vibrato_delay = 0ms }
track third acid { vibrato = 12ct vibrato_rate = 4Hz vibrato_delay = 50ms }
pattern p { 1~ 3 vibrato: 3ct 0ct }
scene main { first = p second = p third = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	document, err := notation.ParseDocument([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	formatted, err := notation.Format(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, ds := notation.Parse(formatted); len(ds) != 0 {
		t.Fatalf("format changed vibrato: %v\n%s", ds, formatted)
	}
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(restored)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"vibrato = 20ct", "vibrato_rate = 6Hz", "vibrato_delay = 150ms", "vibrato = 40ct"} {
		if !strings.Contains(string(text), part) {
			t.Fatalf("source lost %q: %s", part, text)
		}
	}
	cfg, err := CompileEngine(restored, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][3]float32{{20, 6, 150}, {40, 8, 0}, {12, 4, 50}} {
		got := cfg.Track[i].Vibrato
		if got == nil || got.DepthCents != want[0] || got.RateHz != want[1] || got.DelayMS != want[2] {
			t.Fatalf("track %d vibrato = %+v", i, got)
		}
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := kernelimage.Decode(image, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if decoded.Track[i].Vibrato == nil || *decoded.Track[i].Vibrato != *cfg.Track[i].Vibrato {
			t.Fatalf("image lost vibrato on track %d", i)
		}
	}
}

func TestLegacyNamedVibratoParameterRemainsAParameter(t *testing.T) {
	source := `cicada 2
instrument lead { param vibrato = 0.4 voice mono { out = sine(pitch) * vibrato * env(gate, 100ms) } }
track first lead { vibrato = 0.6 }
pattern p { 1 }
scene main { first = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Track[0].Vibrato != nil {
		t.Fatal("legacy synthesis parameter became pitch vibrato")
	}
}

func TestInstrumentVibratoRejectsBadSettings(t *testing.T) {
	source := `cicada 2
instrument lead { vibrato = 20ct vibrato_rate = 6Hz vibrato_delay = 150ms voice mono { out = sine(pitch) * env(gate, 100ms) } }
track first lead { vibrato = 30ct }
pattern p { 1 }
scene main { first = p }
song { main }
`
	for _, bad := range []string{strings.Replace(source, "20ct", "20Hz", 1), strings.Replace(source, "6Hz", "101Hz", 1), strings.Replace(source, "150ms", "-1ms", 1), strings.Replace(source, "30ct", "9601ct", 1), strings.Replace(source, "20ct", "20ct vibrato = 40ct", 1)} {
		if _, ds := notation.Parse([]byte(bad)); len(ds) == 0 {
			t.Fatalf("accepted invalid vibrato: %s", bad)
		}
	}
}

func TestInstrumentVibratoRejectsAuthoredKit(t *testing.T) {
	source := `cicada 2
instrument kick { vibrato = 20ct voice mono { out = sine(pitch) * env(gate, 100ms) } }
kit customkit { bd = kick }
track beat customkit {}
pattern p drums steps=1 { bd: x }
scene main { beat = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	_, ds = Check(score)
	if !hasErrors(ds) {
		t.Fatal("instrument vibrato inside a drum kit should be rejected")
	}
}

func TestInstrumentVibratoChecksCombinedDelayBounds(t *testing.T) {
	source := `cicada 2
instrument pluck { vibrato = 9600ct voice mono { out = comb(noise(), 1 / pitch, 0.8, 0.3) } }
track lead pluck {}
pattern p { c3 - bend: 0ct 0ct }
scene main { lead = p }
song { main }
`
	score, ds := notation.Parse([]byte(source))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, ds := Check(score); !hasErrors(ds) {
		t.Fatal("unsafe baseline vibrato bypassed graph delay bounds")
	}
	safe := strings.Replace(source, "track lead pluck {}", "track lead pluck { vibrato = 0ct }", 1)
	score, ds = notation.Parse([]byte(safe))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, ds := FromScore(score); len(ds) != 0 {
		t.Fatalf("safe track override rejected: %v", ds)
	}
	paused := strings.Replace(source, "track lead pluck {}", "track lead pluck { vibrato_rate = 0Hz }", 1)
	score, ds = notation.Parse([]byte(paused))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, ds := FromScore(score); len(ds) != 0 {
		t.Fatalf("paused vibrato rejected: %v", ds)
	}
	combined := strings.Replace(source, "vibrato = 9600ct", "vibrato = 2400ct", 1)
	combined = strings.Replace(combined, "bend: 0ct 0ct", "bend: 0ct 0ct vibrato: 2400ct 2400ct", 1)
	score, ds = notation.Parse([]byte(combined))
	if len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, ds := Check(score); !hasErrors(ds) {
		t.Fatal("combined baseline and per-note vibrato bypassed delay bounds")
	}
}
