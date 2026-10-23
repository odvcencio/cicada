package project

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

func TestArrangementTempoSourceJSONImageAndTransport(t *testing.T) {
	source, err := os.ReadFile("../examples/arrangement-tempo.cicada")
	if err != nil {
		t.Fatal(err)
	}
	score, ds := notation.Parse(source)
	p, more := FromScore(score)
	if hasErrors(ds) || hasErrors(more) || p == nil {
		t.Fatal(ds, more)
	}
	if got := SongTempoChanges(p); !reflect.DeepEqual(got, []TempoChange{{Bar: 2, BPMMilli: 96000}, {Bar: 4, BPMMilli: 137333}}) {
		t.Fatal(got)
	}
	data, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	text, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	again, ds := notation.Parse(text)
	restored, more := FromScore(again)
	if hasErrors(ds) || hasErrors(more) || !SemanticEqual(p, restored) {
		t.Fatal("tempo source/JSON round trip", ds, more)
	}
	for _, rate := range []int{44100, 48000} {
		cfg, err := CompileEngine(p, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		jsonConfig, err := CompileEngine(decoded, rate, 128)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, jsonConfig) {
			a, b := reflect.ValueOf(cfg), reflect.ValueOf(jsonConfig)
			for i := 0; i < a.NumField(); i++ {
				if !reflect.DeepEqual(a.Field(i).Interface(), b.Field(i).Interface()) {
					t.Errorf("JSON changed engine field %s", a.Type().Field(i).Name)
					if a.Type().Field(i).Name == "Track" {
						for ti := range cfg.Track {
							x, y := reflect.ValueOf(cfg.Track[ti]), reflect.ValueOf(jsonConfig.Track[ti])
							for j := 0; j < x.NumField(); j++ {
								if !reflect.DeepEqual(x.Field(j).Interface(), y.Field(j).Interface()) {
									t.Errorf("Track[%d].%s: %.200v -> %.200v", ti, x.Type().Field(j).Name, x.Field(j).Interface(), y.Field(j).Interface())
								}
							}
						}
					}
				}
			}
			t.FailNow()
		}
		image, err := kernelimage.Encode(cfg)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := kernelimage.Decode(image, rate, 128)
		if err != nil || !reflect.DeepEqual(cfg.Schedule, restored.Schedule) {
			t.Fatal("tempo image", err)
		}
		e, err := engine.New(restored)
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewTempoMap(rate, int64(p.TempoMilli), SongTempoChanges(p))
		if err != nil {
			t.Fatal(err)
		}
		for bar := int64(0); bar <= 6; bar++ {
			if got, want := e.FramesBetweenTicks(0, bar*3840), m.SampleAtTick(bar*3840); got != want {
				t.Fatal(bar, got, want)
			}
		}
		e.Push(cmd.Command{Op: cmd.OpSeek, Track: 0xff, Arg0: 3})
		e.Push(cmd.Command{Op: cmd.OpPlay, Track: 0xff})
		var l, r [128]float32
		e.Render(l[:], r[:])
		if e.LiveClock().BPMMilli != 96000 {
			t.Fatal("seek missed tempo", e.LiveClock())
		}
		e.Close()
	}
}

func TestArrangementTempoRejectsInvalidSteps(t *testing.T) {
	const base = "cicada 2\ntrack bass acid {}\npattern pulse { c2 . }\narrange { place bass bass pulse { at=@1.1.1 length=4bars } TEMPOS }\n"
	for _, tempos := range []string{"tempo @2.2.1 96", "tempo @5.1.1 96", "tempo @2.1.1 96 tempo @2.1.1 120", "tempo @3.1.1 96 tempo @2.1.1 120", "tempo @1.1.1 19", "tempo @1.1.1 301", "tempo @1.1.1 120Hz"} {
		s, ds := notation.Parse([]byte(strings.Replace(base, "TEMPOS", tempos, 1)))
		if !hasErrors(ds) {
			_, ds = FromScore(s)
		}
		if !hasErrors(ds) {
			t.Fatal("accepted", tempos)
		}
	}
}
