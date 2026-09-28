package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestFirstAcidCanonicalJSON(t *testing.T) {
	score := firstScore(t)
	project, diagnostics := FromScore(score)
	if project == nil || len(diagnostics) != 0 {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	if err := ValidateProject(project); err != nil {
		t.Fatal(err)
	}
	if len(project.Tracks) != 3 || project.Tracks[0].Slots[0] == nil || *project.Tracks[0].Slots[0] != "bass-a" || project.Tracks[0].Slots[1] == nil || *project.Tracks[0].Slots[1] != "bass-b" {
		t.Fatalf("wrong slot allocation: %+v", project.Tracks[0].Slots)
	}
	if len(project.Patterns[3].Lanes) != 11 || len(project.Patterns[3].Lanes["rs"]) != 16 {
		t.Fatal("omitted drum lanes did not become explicit rests")
	}
	encoded, err := CanonicalJSON(project)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(encoded, []byte("\n")) || bytes.Contains(encoded, []byte("\r")) || bytes.ContainsAny(encoded, "eE+") && strings.Contains(string(encoded), "1e-") {
		t.Fatal("unexpected JSON number or line ending")
	}
	if !bytes.HasPrefix(encoded, []byte("{\n  \"edition\": 1,\n  \"effects\": []")) {
		t.Fatalf("object keys are not sorted:\n%s", encoded[:min(len(encoded), 120)])
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(project, decoded) {
		t.Fatal("canonical JSON changed the semantic project")
	}
	again, err := CanonicalJSON(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("canonical JSON is not stable: %v", err)
	}
}

func TestProject1ExampleJSONRoundTripsStayByteStable(t *testing.T) {
	count := 0
	err := filepath.WalkDir("../examples", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".cicada" {
			return nil
		}
		count++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		score, diagnostics := notation.Parse(source)
		if score == nil || hasProjectErrors(diagnostics) {
			return fmt.Errorf("%s: parse failed: %+v", path, diagnostics)
		}
		p, diagnostics := FromScore(score)
		if p == nil || hasProjectErrors(diagnostics) {
			return fmt.Errorf("%s: compile failed: %+v", path, diagnostics)
		}
		first, err := CanonicalJSON(p)
		if err != nil {
			return fmt.Errorf("%s: encode: %w", path, err)
		}
		if p.Format != FormatID || p.Version != 1 {
			return fmt.Errorf("%s: no /2-only content should be emitted as /1", path)
		}
		decoded, err := DecodeJSON(first)
		if err != nil {
			return fmt.Errorf("%s: decode: %w", path, err)
		}
		second, err := CanonicalJSON(decoded)
		if err != nil || !bytes.Equal(first, second) {
			return fmt.Errorf("%s: project/1 output changed after decode: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no project examples were tested")
	}
}

func TestProject2SceneSettingsRoundTripInSourceOrderAndBaseUnits(t *testing.T) {
	source := []byte("fx delay { feedback = 0.2 }\ntrack bass acid { cutoff = 700Hz send_a = 0.2 }\ntrack drums drums {}\npattern riff acid steps=1 { 1 }\npattern beat drums steps=1 { bd: x }\nscene drop { bass=riff drums=beat bass.cutoff=0.9kHz bass.send.delay=0.3 delay.feedback=0.4 drums.level=off }\nsong { drop }\n")
	score, diagnostics := notation.Parse(source)
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("parse scene settings: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) || p.Format != FormatID2 || p.Version != 2 {
		t.Fatalf("compile scene settings as project/2: %+v, %+v", p, diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"format": "cicada.project/2"`)) || !bytes.Contains(encoded, []byte(`"settings": [`)) {
		t.Fatalf("project/2 scene settings missing: %s", encoded)
	}
	var raw struct {
		Scenes []struct {
			Settings []struct {
				Path  string         `json:"path"`
				Value map[string]any `json:"value"`
			} `json:"settings"`
		} `json:"scenes"`
	}
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	settings := raw.Scenes[0].Settings
	if len(settings) != 4 || settings[0].Path != "bass.cutoff" || settings[0].Value["unit"] != "hz" || settings[0].Value["number"] != float64(900) || settings[1].Path != "bass.send.delay" || settings[2].Path != "delay.feedback" || settings[3].Path != "drums.level" {
		t.Fatalf("settings order or base-unit conversion changed: %+v", settings)
	}
	if len(settings[0].Value) != 2 || settings[0].Value["text"] != nil {
		t.Fatalf("numeric /2 value must contain only number and unit: %+v", settings[0].Value)
	}
	if len(settings[3].Value) != 2 || settings[3].Value["unit"] != "enum" || settings[3].Value["text"] != "off" || settings[3].Value["number"] != nil {
		t.Fatalf("enum /2 value has the wrong shape: %+v", settings[3].Value)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || !reflect.DeepEqual(p, decoded) {
		t.Fatalf("project/2 JSON round trip: %v", err)
	}
	again, err := CanonicalJSON(decoded)
	if err != nil || !bytes.Equal(encoded, again) {
		t.Fatalf("project/2 JSON is not canonical: %v", err)
	}
}

func TestMigrate1To2IsPureAndWriterKeepsProject1WithoutNewContent(t *testing.T) {
	p, diagnostics := FromScore(firstScore(t))
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	before := *p
	beforeBytes, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	migrated, err := Migrate1To2(p)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Format != FormatID2 || migrated.Version != 2 || !reflect.DeepEqual(p, &before) {
		t.Fatalf("migration mutated source or lost the v2 marker: source=%s/%d migrated=%s/%d", p.Format, p.Version, migrated.Format, migrated.Version)
	}
	afterBytes, err := CanonicalJSON(migrated)
	if err != nil || !bytes.Equal(beforeBytes, afterBytes) || !bytes.Contains(afterBytes, []byte(`"format": "cicada.project/1"`)) {
		t.Fatalf("project without /2 content changed its JSON output: %v", err)
	}
	decoded, err := DecodeJSON(beforeBytes)
	if err != nil || decoded.Format != FormatID || decoded.Version != 1 {
		t.Fatalf("project/1 reader rejected canonical output: %v", err)
	}
	if _, err := Migrate1To2(migrated); err == nil {
		t.Fatal("migration accepted a project/2 input")
	}
}

func TestMigrate1To2CarriesLegacyMixerFields(t *testing.T) {
	source := []byte("fx delay { feedback = 0.2 }\ntrack bass acid { send_a = 0.25 send_pre = true }\npattern pulse acid steps=1 { 1 }\nscene main { bass = pulse }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	if hasProjectErrors(diagnostics) {
		t.Fatalf("source parse: %+v", diagnostics)
	}
	legacy, diagnostics := FromScore(score)
	if legacy == nil || hasProjectErrors(diagnostics) || legacy.Format != FormatID {
		t.Fatalf("source project: %+v", diagnostics)
	}
	migrated, err := Migrate1To2(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated.Tracks[0].Mixer.Sends) != 1 {
		t.Fatalf("legacy send missing after migration: %+v", migrated.Tracks[0].Mixer)
	}
	send := migrated.Tracks[0].Mixer.Sends[0]
	if send.To != "delay" || send.Level.Number == nil || *send.Level.Number != .25 || send.Tap != "pre" {
		t.Fatalf("legacy send changed in /2 migration: %+v", send)
	}
	encoded, err := CanonicalJSON(migrated)
	if err != nil || !bytes.Contains(encoded, []byte(`"format": "cicada.project/1"`)) {
		t.Fatalf("writer promoted a project with no /2-only content: %v", err)
	}
	if _, err := DecodeJSON(encoded); err != nil {
		t.Fatalf("legacy writer output did not decode: %v", err)
	}
}

func TestMigrate1To2PlacesLegacyCompressorOnMusicBus(t *testing.T) {
	source := []byte("fx comp { threshold = -20dB }\ntrack bass acid {}\npattern pulse acid steps=1 { 1 }\nscene main { bass = pulse }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	if hasProjectErrors(diagnostics) {
		t.Fatalf("source parse: %+v", diagnostics)
	}
	legacy, diagnostics := FromScore(score)
	if legacy == nil || hasProjectErrors(diagnostics) || legacy.Format != FormatID {
		t.Fatalf("source project: %+v", diagnostics)
	}
	migrated, err := Migrate1To2(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated.Buses) != 1 || migrated.Buses[0].ID != "music" || len(migrated.Buses[0].Mixer.Inserts) != 1 || migrated.Buses[0].Mixer.Inserts[0] != "comp" {
		t.Fatalf("legacy compressor route was not made explicit: %+v", migrated.Buses)
	}
	encoded, err := CanonicalJSON(migrated)
	if err != nil || !bytes.Contains(encoded, []byte(`"format": "cicada.project/2"`)) || !bytes.Contains(encoded, []byte(`"kind": "comp"`)) {
		t.Fatalf("migrated compressor project is not /2: %v", err)
	}
}

func TestNamedMixerProject2JSONRoundTripAndLowering(t *testing.T) {
	const source = `fx warm drive {}
fx room delay {}
fx hall reverb {}
fx glue comp {}
track bass acid {
  level = -6dB
  pan = 0.2
  mute = off
  solo = off
  insert = warm
  send room = 0.3 pre
  send hall = -12dB
  out = sfx
}

bus music { level = -3dB insert = glue mute = on }
bus sfx { solo = on }
master { level = -1dB insert = none mute = on solo = on }
export web { rate = 44100Hz bits = 24 tail = 1s loudness = -14LUFS true_peak = -1dBTP }
pattern pulse acid steps=1 { 1 }
scene main { bass = pulse }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) || p.Format != FormatID2 {
		t.Fatalf("named mixer compile: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"format": "cicada.project/2"`, `"kind": "delay"`, `"buses"`, `"master"`, `"exports"`, `"tap": "pre"`, `"out": "sfx"`} {
		if !bytes.Contains(encoded, []byte(required)) {
			t.Errorf("/2 JSON missing %s:\n%s", required, encoded)
		}
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(decoded)
	if err != nil || !bytes.Equal(encoded, second) || !SemanticEqual(p, decoded) {
		t.Fatalf("/2 JSON round trip changed named mixer project: %v", err)
	}
	cfg, err := CompileEngine(decoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Track[0].SendAPre || cfg.Track[0].SendBPre || !cfg.Track[0].BusSFX || cfg.CompMusic == nil || cfg.MasterGainDB != -1 || !cfg.MusicBusMute || !cfg.SFXBusSolo || !cfg.MasterMute || !cfg.MasterSolo {
		t.Fatalf("named mixer did not lower to today's engine config: %+v", cfg.Track[0])
	}
	if decoded.Exports[0].Rate == nil || *decoded.Exports[0].Rate != 44_100 || decoded.Exports[0].Loudness == nil || decoded.Exports[0].TruePeak == nil {
		t.Fatalf("export profile lost during /2 round trip: %+v", decoded.Exports)
	}
}

func TestMixerOffLevelKeepsItsDefaultGainAcrossJSON(t *testing.T) {
	source := []byte("track bass acid {}\nbus music { level = off }\nmaster { level = off }\npattern pulse acid steps=1 { 1 }\nscene main { bass = pulse }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("off-level compile: %+v", diagnostics)
	}
	if p.Buses[0].Mixer.GainDB != -3 || !p.Buses[0].Mixer.Mute || p.Master.Mixer.GainDB != 0 || !p.Master.Mixer.Mute {
		t.Fatalf("off levels changed their stored defaults: bus=%+v master=%+v", p.Buses[0].Mixer, p.Master.Mixer)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Buses[0].Mixer.GainDB != -3 || !decoded.Buses[0].Mixer.Mute || decoded.Master.Mixer.GainDB != 0 || !decoded.Master.Mixer.Mute {
		t.Fatalf("off levels changed through /2 JSON: bus=%+v master=%+v", decoded.Buses[0].Mixer, decoded.Master.Mixer)
	}
	decoded.Tracks[0].Mixer.Level = &Value{Unit: "lufs", Number: floatPointer(-14)}
	if _, err := CanonicalJSON(decoded); err == nil {
		t.Fatal("accepted loudness as a mixer level")
	}
}

func floatPointer(value float64) *float64 { return &value }

func hasProjectErrors(diagnostics []notation.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

func TestProjectJSONRecordsSourceEditionAndReadsLegacyJSON(t *testing.T) {
	p, diagnostics := FromScore(firstScore(t))
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"edition": 1`)) {
		t.Fatal("canonical JSON omitted source edition")
	}
	var legacy map[string]any
	if err := json.Unmarshal(encoded, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "edition")
	oldJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(oldJSON)
	if err != nil || decoded.Edition != 1 {
		t.Fatalf("legacy project edition = %v, %v", decoded, err)
	}
	if !reflect.DeepEqual(p, decoded) {
		t.Fatal("legacy JSON changed musical meaning")
	}
	for _, edition := range []any{0, nil} {
		legacy["edition"] = edition
		invalid, err := json.Marshal(legacy)
		if err != nil {
			t.Fatal(err)
		}
		_, err = DecodeJSON(invalid)
		var diagnostic *JSONError
		if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-VERSION" || diagnostic.Pointer != "/edition" {
			t.Fatalf("accepted unsupported edition %v: %v", edition, err)
		}
	}
	legacy["edition"] = 2
	editionTwo, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = DecodeJSON(editionTwo)
	if err != nil || decoded.Edition != 2 {
		t.Fatalf("edition 2 project JSON = %+v, %v", decoded, err)
	}
}

func TestSourceEditionTwoRoundTripsThroughProjectAndSource(t *testing.T) {
	source := []byte("cicada 2\ntrack bass acid {}\npattern pulse acid { 1 }\nscene main { bass=pulse }\nsong { main }\n")
	score, diagnostics := notation.Parse(source)
	if score == nil || hasProjectErrors(diagnostics) {
		t.Fatalf("edition 2 parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || hasProjectErrors(diagnostics) || p.Edition != 2 {
		t.Fatalf("edition 2 lowering: project=%+v diagnostics=%+v", p, diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil || decoded.Edition != 2 {
		t.Fatalf("edition 2 JSON round trip: project=%+v err=%v", decoded, err)
	}
	rewritten, err := ToSource(decoded)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, diagnostics := notation.Parse(rewritten)
	if reparsed == nil || hasProjectErrors(diagnostics) || reparsed.Version != 2 {
		t.Fatalf("edition 2 source round trip: %+v; %s", diagnostics, rewritten)
	}
}

func TestRejectsDuplicateJSONKey(t *testing.T) {
	if _, err := DecodeJSON([]byte(`{"format":"cicada.project/1","format":"cicada.project/1"}`)); err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("want duplicate-key error, got %v", err)
	}
}

func TestJSONBodySizeBoundary(t *testing.T) {
	p, diagnostics := FromScore(firstScore(t))
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	boundary := append(bytes.Clone(encoded), bytes.Repeat([]byte(" "), maxJSONBytes-len(encoded))...)
	if _, err := DecodeJSON(boundary); err != nil {
		t.Fatalf("decoder rejected exactly 2 MiB: %v", err)
	}
	if _, err := DecodeJSON(append(boundary, ' ')); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatalf("decoder accepted a body above 2 MiB: %v", err)
	} else {
		var diagnostic *JSONError
		if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-LIMIT" {
			t.Fatalf("size error lost its diagnostic code: %v", err)
		}
	}
}

func TestJSONDiagnosticLocations(t *testing.T) {
	for _, test := range []struct {
		name, input, code, pointer string
		line, column               int
	}{
		{"duplicate nested key", `{"a/b~c":{"x":1,"x":2}}`, "CICADA-DUPLICATE", "/a~1b~0c/x", 1, 17},
		{"syntax after unicode", "{\n  \"title\": \"🎵\",\n  \"x\": }\n", "CICADA-SYNTAX", "/x", 3, 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeJSON([]byte(test.input))
			var diagnostic *JSONError
			if !errors.As(err, &diagnostic) {
				t.Fatalf("missing JSON diagnostic: %v", err)
			}
			if diagnostic.Code != test.code || diagnostic.Pointer != test.pointer || diagnostic.Line != test.line || diagnostic.Column != test.column {
				t.Fatalf("diagnostic = %s %s %d:%d (%v), want %s %s %d:%d", diagnostic.Code, diagnostic.Pointer, diagnostic.Line, diagnostic.Column, diagnostic.Cause, test.code, test.pointer, test.line, test.column)
			}
		})
	}
	p, diagnostics := FromScore(firstScore(t))
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	invalidVersion := bytes.Replace(encoded, []byte(`"version": 1`), []byte(`"version": 2`), 1)
	if bytes.Equal(invalidVersion, encoded) {
		t.Fatal("canonical JSON did not include a version field")
	}
	_, err = DecodeJSON(invalidVersion)
	var diagnostic *JSONError
	if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-VERSION" || diagnostic.Pointer != "/version" || diagnostic.Line < 2 || diagnostic.Column != 3 {
		t.Fatalf("future version diagnostic: %v", err)
	}
}

func TestJSONFieldPaths(t *testing.T) {
	p, diagnostics := FromScore(firstScore(t))
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, pointer string
		change        func(map[string]any)
	}{
		{"missing step field", "/patterns/0/data/0/velocity", func(root map[string]any) {
			pattern := root["patterns"].([]any)[0].(map[string]any)
			delete(pattern["data"].([]any)[0].(map[string]any), "velocity")
		}},
		{"unknown track field", "/tracks/0/mystery", func(root map[string]any) {
			root["tracks"].([]any)[0].(map[string]any)["mystery"] = true
		}},
		{"unknown root field", "/mystery", func(root map[string]any) {
			root["mystery"] = true
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal(encoded, &root); err != nil {
				t.Fatal(err)
			}
			test.change(root)
			data, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 32; attempt++ {
				_, err := DecodeJSON(data)
				var diagnostic *JSONError
				if !errors.As(err, &diagnostic) || diagnostic.Code != "CICADA-PARAM" || diagnostic.Pointer != test.pointer {
					t.Fatalf("attempt %d: expected %s, got %v", attempt, test.pointer, err)
				}
				if strings.HasPrefix(test.name, "unknown") && (diagnostic.Line != 1 || diagnostic.Column < 2) {
					t.Fatalf("attempt %d: unknown field lost its key position: %d:%d", attempt, diagnostic.Line, diagnostic.Column)
				}
			}
		})
	}
}

func TestJSONRejectsUncompilableCustomInstrumentOverride(t *testing.T) {
	score := firstScore(t)
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	value := 300.0
	p.Tracks[2].Params["ghost"] = Value{Unit: "hz", Number: &value}
	if _, err := CanonicalJSON(p); err == nil {
		t.Fatal("canonical writer accepted an unrenderable custom parameter")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(raw); err == nil {
		t.Fatal("JSON decoder accepted an unrenderable custom parameter")
	}
}

func TestJSONRejectsUnboundInstrumentSymbol(t *testing.T) {
	score := firstScore(t)
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	p.Instruments[0].Out = Expr{Op: "saw", Args: []Expr{{Name: "missing"}}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(raw); err == nil {
		t.Fatal("JSON decoder accepted an unbound graph symbol")
	}
}

func TestJSONRejectsStepThatSourceCannotRepresent(t *testing.T) {
	score := firstScore(t)
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	p.Patterns[0].Data[0].Probability = 0
	if _, err := CanonicalJSON(p); err == nil {
		t.Fatal("canonical writer accepted a step with no source v1 spelling")
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeJSON(raw); err == nil {
		t.Fatal("JSON decoder accepted a step with no source v1 spelling")
	}
}

func TestCanonicalJSONLimitMatchesSourceValidation(t *testing.T) {
	var source strings.Builder
	source.WriteString("cicada 1\n")
	for track := 0; track < 16; track++ {
		fmt.Fprintf(&source, "track t%d acid {}\n", track)
	}
	steps := strings.TrimSpace(strings.Repeat("1 ", 32))
	for track := 0; track < 16; track++ {
		for slot := 0; slot < 16; slot++ {
			fmt.Fprintf(&source, "pattern p%d_%d acid steps=32 slot=%d { %s }\n", track, slot, slot, steps)
		}
	}
	for slot := 0; slot < 16; slot++ {
		fmt.Fprintf(&source, "scene s%d {", slot)
		for track := 0; track < 16; track++ {
			fmt.Fprintf(&source, " t%d=p%d_%d", track, track, slot)
		}
		source.WriteString(" }\n")
	}
	source.WriteString("song {")
	for slot := 0; slot < 16; slot++ {
		fmt.Fprintf(&source, " s%d", slot)
	}
	source.WriteString(" }\n")
	score, diagnostics := notation.Parse([]byte(source.String()))
	if len(diagnostics) != 0 {
		t.Fatalf("dense source parse: %+v", diagnostics)
	}
	p, diagnostics := FromScore(score)
	if p == nil || len(diagnostics) != 0 {
		t.Fatalf("32-step project should fit: %+v", diagnostics)
	}
	encoded, err := CanonicalJSON(p)
	if err != nil || len(encoded) > maxJSONBytes {
		t.Fatalf("32-step canonical project: %d bytes, %v", len(encoded), err)
	}
	if _, err := DecodeJSON(encoded); err != nil {
		t.Fatalf("decoder refused canonical project: %v", err)
	}
	for i := range p.Patterns {
		step := *p.Patterns[i].Data[0]
		p.Patterns[i].Steps = 64
		p.Patterns[i].Data = make([]*Step, 64)
		for j := range p.Patterns[i].Data {
			copy := step
			p.Patterns[i].Data[j] = &copy
		}
	}
	if _, err := CanonicalJSON(p); !errors.Is(err, errCanonicalJSONLimit) {
		t.Fatalf("writer accepted project its decoder cannot read: %v", err)
	}
	compact, err := json.Marshal(p)
	if err != nil || len(compact) > maxJSONBytes {
		t.Fatalf("dense compact JSON must fit the input bound: %d bytes, %v", len(compact), err)
	}
	_, err = DecodeJSON(compact)
	var sizeDiagnostic *JSONError
	if !errors.As(err, &sizeDiagnostic) || sizeDiagnostic.Code != "CICADA-LIMIT" {
		t.Fatalf("compact project with oversized canonical form: %v", err)
	}
	tooLarge := strings.ReplaceAll(source.String(), "steps=32", "steps=64")
	// Build the 64-cell source from the same valid layout.
	longSteps := strings.TrimSpace(strings.Repeat("1 ", 64))
	tooLarge = strings.ReplaceAll(tooLarge, steps, longSteps)
	largeScore, diagnostics := notation.Parse([]byte(tooLarge))
	if len(diagnostics) != 0 {
		t.Fatalf("64-step source parse: %+v", diagnostics)
	}
	if compiled, diagnostics := FromScore(largeScore); compiled != nil || len(diagnostics) != 1 || diagnostics[0].Code != "CICADA-LIMIT" {
		t.Fatalf("source accepted an unroundtrippable project: %+v", diagnostics)
	}
}
