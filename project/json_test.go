package project

import (
	"bytes"
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
