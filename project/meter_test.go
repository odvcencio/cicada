package project

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

const waltzSource = `cicada 2
tempo 132
meter 7/8 2+2+3
track bass acid {}
pattern seven { step = 1/8 c2 g2 c3 g2 c3 eb3 g3 }
scene a { bass = seven }
song { a*4 }
`

// A meter travels from source through project JSON /3, back to source, into
// the engine configuration and through a kernel image.
func TestMeterSourceJSONImageRoundTrip(t *testing.T) {
	score, ds := notation.Parse([]byte(waltzSource))
	if score == nil || hasErrors(ds) {
		t.Fatalf("parse: %v", ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) {
		t.Fatalf("project: %v", ds)
	}
	if p.Format != FormatID3 || p.Version != 3 || p.Meter == nil || p.Meter.Numerator != 7 || p.Meter.Denominator != 8 || len(p.Meter.Groups) != 3 {
		t.Fatalf("project meter: %s/%d %+v", p.Format, p.Version, p.Meter)
	}
	if got := p.MeterMap().BarTicks(0); got != 3360 || p.SongEndTick() != 4*3360 {
		t.Fatalf("bar %d, song end %d", got, p.SongEndTick())
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil || root["format"] != FormatID3 || root["meter"] == nil {
		t.Fatalf("JSON: %s", encoded)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || decoded.Meter == nil || decoded.Meter.Numerator != 7 {
		t.Fatalf("decode: %v %+v", err, decoded)
	}
	source, err := ToSource(decoded)
	if err != nil || !bytes.Contains(source, []byte("\nmeter 7/8 2+2+3\n")) {
		t.Fatalf("source: %s: %v", source, err)
	}
	cfg, err := CompileEngine(decoded, 48000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Meters) != 1 || cfg.Meters[0].Meter.Beats != 7 || cfg.Meters[0].Meter.Unit != 8 {
		t.Fatalf("engine meters: %+v", cfg.Meters)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if flags := binary.LittleEndian.Uint32(image[8:]); flags&kernelimage.MeterFlag == 0 {
		t.Fatalf("missing meter flag: %#x", flags)
	}
	back, err := kernelimage.Decode(image, 48000, 128)
	if err != nil || len(back.Meters) != 1 || back.Meters[0] != cfg.Meters[0] {
		t.Fatalf("image round trip: %v %+v", err, back.Meters)
	}
	e, err := engine.New(back)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := e.MeterMap().BarTicks(0); got != 3360 {
		t.Fatalf("engine bar %d", got)
	}
}

// A meter is allowed in edition 1 too, and a track level survives JSON /3.
func TestEditionOneMeterRoundTrip(t *testing.T) {
	text := "meter 5/4\ntempo 100\ntrack bass acid { level = -18dB }\npattern five { step = 1/4 c2 g2 c3 g2 c3 }\nscene a { bass = five }\nsong { a*2 }\n"
	score, ds := notation.Parse([]byte(text))
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) || p.Format != FormatID3 {
		t.Fatalf("%v %+v", ds, p)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	a, errA := CompileEngine(p, 48000, 128)
	b, errB := CompileEngine(decoded, 48000, 128)
	if errA != nil || errB != nil || a.Track[0].GainDB != -18 || b.Track[0].GainDB != -18 {
		t.Fatalf("level through JSON: %v %v %v %v", errA, errB, a.Track[0].GainDB, b.Track[0].GainDB)
	}
	source, err := ToSource(decoded)
	if err != nil || !bytes.Contains(source, []byte("meter 5/4")) {
		t.Fatalf("source: %s %v", source, err)
	}
	again, ds := notation.Parse(source)
	if again == nil || hasErrors(ds) || again.Meter == nil || again.Meter.Beats != 5 {
		t.Fatalf("source does not parse back: %v\n%s", ds, source)
	}
}

// A song without a meter keeps its format, its JSON and its image bytes.
func TestNoMeterKeepsFormatAndImage(t *testing.T) {
	plain := strings.Replace(waltzSource, "meter 7/8 2+2+3\n", "", 1)
	score, ds := notation.Parse([]byte(plain))
	if score == nil || hasErrors(ds) {
		t.Fatal(ds)
	}
	p, ds := FromScore(score)
	if p == nil || hasErrors(ds) || p.Meter != nil || p.Format == FormatID3 {
		t.Fatalf("%v %+v", ds, p)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil || bytes.Contains(encoded, []byte(`"meter"`)) || bytes.Contains(encoded, []byte("project/3")) {
		t.Fatalf("JSON: %s %v", encoded, err)
	}
	cfg, err := CompileEngine(p, 48000, 128)
	if err != nil || len(cfg.Meters) != 0 {
		t.Fatalf("%v %+v", err, cfg.Meters)
	}
	image, err := kernelimage.Encode(cfg)
	if err != nil || binary.LittleEndian.Uint32(image[8:])&kernelimage.MeterFlag != 0 {
		t.Fatalf("a 4/4 image must carry no meter table: %v", err)
	}
}

func TestProjectMeterValidation(t *testing.T) {
	for _, c := range []struct {
		meter Meter
		want  string
	}{
		{Meter{Numerator: 3, Denominator: 5}, "CICADA-METER"},
		{Meter{Numerator: 0, Denominator: 4}, "CICADA-METER"},
		{Meter{Numerator: 32, Denominator: 2}, "CICADA-LIMIT"},
		{Meter{Numerator: 7, Denominator: 8, Groups: []int{2, 2, 2}}, "CICADA-METER"},
	} {
		m := c.meter
		if err := validMeter(&m); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %s", c.meter, err, c.want)
		}
	}
	p := &Project{Format: FormatID2, Version: 2, Meter: &Meter{Numerator: 3, Denominator: 4}}
	if err := ValidateProject(p); err == nil || !strings.Contains(err.Error(), "cicada.project/3") {
		t.Fatalf("a meter in /2: %v", err)
	}
}

// An automation position outside its bar names the bar and the meter's
// ranges, as an arrangement position does, not a 4/4 range (#358 review).
func TestAutomationPositionErrorsNameTheMeter(t *testing.T) {
	for _, c := range []struct{ meter, at, want string }{
		{"3/4", "@2.4.1", "@2.4.1 is outside bar 2, which is in 3/4 (beats 1 to 3, steps 1 to 4)"},
		{"7/8", "@2.7.3", "@2.7.3 is outside bar 2, which is in 7/8 (beats 1 to 7, steps 1 to 2)"},
	} {
		source := "cicada 2\nmeter " + c.meter + "\ntrack bass acid {}\npattern p { c2 }\nscene a { bass = p }\nsong { a*4 }\nautomate bass.cutoff {\n  @1.1.1 400Hz\n  " + c.at + " 800Hz\n}\n"
		score, ds := notation.Parse([]byte(source))
		if score == nil || hasErrors(ds) {
			t.Fatal(ds)
		}
		_, ds = FromScore(score)
		found := false
		for _, d := range ds {
			found = found || d.Severity == "error" && strings.Contains(d.Message, c.want)
		}
		if !found {
			t.Errorf("%s %s: want %q, got %v", c.meter, c.at, c.want, ds)
		}
	}
}
