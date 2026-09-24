package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	if !bytes.HasPrefix(encoded, []byte("{\n  \"effects\": []")) {
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
