package project

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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

func TestJSONCustomInstrumentRendersZeroProbabilityStep(t *testing.T) {
	score := firstScore(t)
	p, diagnostics := FromScore(score)
	if p == nil {
		t.Fatalf("project compilation: %+v", diagnostics)
	}
	p.Patterns[0].Data[0].Probability = 0
	encoded, err := CanonicalJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileEngine(decoded, 48_000, 256); err != nil {
		t.Fatalf("schema-valid project could not compile: %v", err)
	}
}
