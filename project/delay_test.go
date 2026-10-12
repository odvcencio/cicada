package project

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/notation"
)

func TestDelaySendSourceProjectAndEngineRoundTrip(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "testdata", "edition1", "examples", "fx", "delay-send.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("source: %+v", diagnostic)
		}
	}
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	if len(p.Effects) != 1 || p.Effects[0].ID != "delay" || p.Tracks[0].Mixer.SendA != .4 || p.Tracks[1].Mixer.SendA != 0 {
		t.Fatalf("delay route changed during lowering: %+v %+v", p.Effects, p.Tracks)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(rewritten, []byte("time = 1/8T")) || !bytes.Contains(rewritten, []byte("send_a = 0.4")) {
		t.Fatalf("source round trip lost delay: %s", rewritten)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DelayA == nil || cfg.DelayA.Division != fx.EighthTriplet || cfg.Track[0].SendA != .4 {
		t.Fatalf("live delay route is wrong: %+v %+v", cfg.DelayA, cfg.Track[0])
	}
	if _, err := engine.New(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDelayTimeSourceSpellingsAndMissingReturn(t *testing.T) {
	base := "cicada 1\nfx delay { time = 1/8T }\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
	for _, spelling := range []string{"1/32", "1/16", "1/16T", "1/16.", "1/8", "1/8T", "1/8.", "3/16", "1/4", "1/4.", "1/2", "250ms"} {
		source := bytes.Replace([]byte(base), []byte("1/8T"), []byte(spelling), 1)
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("time %s did not parse: %+v", spelling, diagnostic)
			}
		}
		if p, extra := FromScore(score); p == nil {
			t.Fatalf("time %s did not compile: %+v", spelling, extra)
		}
	}
	for _, invalid := range []string{"0.5", "1/64", "2500ms", "1/16t"} {
		source := bytes.Replace([]byte(base), []byte("1/8T"), []byte(invalid), 1)
		score, diagnostics := notation.Parse(source)
		if score != nil {
			if p, extra := FromScore(score); p != nil {
				t.Fatalf("accepted invalid time %s", invalid)
			} else {
				diagnostics = append(diagnostics, extra...)
			}
		}
		if len(diagnostics) == 0 {
			t.Fatalf("missing diagnostic for time %s", invalid)
		}
	}
	score, diagnostics := notation.Parse([]byte("cicada 1\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"))
	if score == nil || len(diagnostics) == 0 || diagnostics[0].Code != "CICADA-REFERENCE" {
		t.Fatalf("missing delay reference diagnostic: %+v", diagnostics)
	}
}

func TestDelayPingPongAcceptsSwitchSpellings(t *testing.T) {
	for spelling, want := range map[string]bool{"on": true, "true": true, "off": false, "false": false} {
		source := "cicada 1\nfx echo delay { pingpong = " + spelling + " }\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
		score, diagnostics := notation.Parse([]byte(source))
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("pingpong %s: %+v", spelling, diagnostic)
			}
		}
		p, extra := FromScore(score)
		if p == nil {
			t.Fatalf("pingpong %s did not compile: %+v", spelling, extra)
		}
		cfg, err := CompileEngine(p, 48_000, 128)
		if err != nil {
			t.Fatalf("pingpong %s: %v", spelling, err)
		}
		if cfg.DelayA == nil || cfg.DelayA.PingPong != want {
			t.Fatalf("pingpong %s: got %+v", spelling, cfg.DelayA)
		}
	}
}

func TestDelayExtendedDivisions(t *testing.T) {
	base := "cicada 1\nfx delay { time = 1/8T }\ntrack bass acid { send_a = 0.4 }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
	for _, spelling := range []string{"3/8", "1/4T", "1/2.", "1/2T", "1/32T", "1/32."} {
		source := bytes.Replace([]byte(base), []byte("1/8T"), []byte(spelling), 1)
		score, diagnostics := notation.Parse(source)
		for _, diagnostic := range diagnostics {
			if diagnostic.Severity == "error" {
				t.Fatalf("time %s did not parse: %+v", spelling, diagnostic)
			}
		}
		p, extra := FromScore(score)
		if p == nil {
			t.Fatalf("time %s did not compile: %+v", spelling, extra)
		}
		if _, err := CompileEngine(p, 48_000, 128); err != nil {
			t.Fatalf("time %s: %v", spelling, err)
		}
	}
	if got := delayDivisionMilliseconds("3/8", 120_000); got != 750 {
		t.Fatalf("3/8 at 120 BPM = %v ms, want 750", got)
	}
	// 1/2. is three beats: 4.5 s at 40 BPM exceeds the four-second buffer.
	if err := (fx.DelayParams{Division: mustDivision(t, "1/2."), Feedback: .35, DampHz: 6000, Width: 1, Mix: 1}).ValidateTempo(40_000); err == nil {
		t.Fatal("1/2. at 40 BPM must not fit the buffer")
	}
}

func mustDivision(t *testing.T, name string) fx.DelayDivision {
	t.Helper()
	d, err := fx.ParseDelayDivision(name)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// Canonical exports keep the spelling written in the score, so each schema's
// delay enums must list every spelling the language accepts.
func TestDelaySpellingsExportWithinSchemaEnums(t *testing.T) {
	enumAt := func(t *testing.T, path, param string) map[string]bool {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Defs map[string]json.RawMessage `json:"$defs"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatal(err)
		}
		var delay struct {
			Properties struct {
				Params struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"params"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(schema.Defs["delay_effect"], &delay); err != nil {
			t.Fatal(err)
		}
		raw := delay.Properties.Params.Properties[param]
		var found []string
		var walk func(any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				if list, ok := v["enum"].([]any); ok {
					for _, item := range list {
						if s, ok := item.(string); ok {
							found = append(found, s)
						}
					}
				}
				for _, child := range v {
					walk(child)
				}
			case []any:
				for _, child := range v {
					walk(child)
				}
			}
		}
		var tree any
		if err := json.Unmarshal(raw, &tree); err != nil {
			t.Fatal(err)
		}
		walk(tree)
		set := map[string]bool{}
		for _, s := range found {
			set[s] = true
		}
		return set
	}
	for _, edition := range []struct{ version, schema string }{{"1", "schema/project-1.json"}, {"2", "schema/project-2.json"}} {
		pingpong := enumAt(t, edition.schema, "pingpong")
		time := enumAt(t, edition.schema, "time")
		for _, spelling := range []string{"on", "off", "true", "false"} {
			send := "send_a = 0.4"
			if edition.version == "2" {
				send = "send echo = 0.4"
			}
			source := "cicada " + edition.version + "\nfx echo delay { pingpong = " + spelling + " }\ntrack bass acid { " + send + " }\npattern riff acid steps=1 { 1 }\nscene main { bass=riff }\nsong { main }\n"
			score, diagnostics := notation.Parse([]byte(source))
			if hasErrors(diagnostics) {
				t.Fatalf("edition %s pingpong %s: %+v", edition.version, spelling, diagnostics)
			}
			p, extra := FromScore(score)
			if p == nil {
				t.Fatalf("edition %s pingpong %s: %+v", edition.version, spelling, extra)
			}
			encoded, err := CanonicalJSON(p)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Effects []struct {
					Params map[string]struct {
						Text string `json:"text"`
					} `json:"params"`
				} `json:"effects"`
			}
			if err := json.Unmarshal(encoded, &doc); err != nil {
				t.Fatal(err)
			}
			got := doc.Effects[0].Params["pingpong"].Text
			if !pingpong[got] {
				t.Errorf("edition %s: exported pingpong %q (written %q) is not in %s", edition.version, got, spelling, edition.schema)
			}
		}
		for index := 1; ; index++ {
			division := fx.DelayDivision(index)
			if division.String() == "invalid" {
				break
			}
			if !time[division.String()] {
				t.Errorf("%s does not list delay division %s", edition.schema, division)
			}
		}
	}
}
