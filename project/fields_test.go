package project

import (
	"bytes"
	"os"
	"testing"
)

func TestFieldCatalogMatchesCheckedInArtifact(t *testing.T) {
	data, err := FieldsJSON()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile("schema/cicada.fields-1.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(data, '\n'), artifact) {
		t.Fatal("field catalog changed; regenerate with go run ./cmd/cicada fields > project/schema/cicada.fields-1.json")
	}
	catalog, err := Fields()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Format != "cicada.fields/1" || len(catalog.Fields) < 70 {
		t.Fatalf("incomplete field catalog: %s, %d fields", catalog.Format, len(catalog.Fields))
	}
	if len(catalog.Constructs) != 15 {
		t.Fatalf("expected 15 semantic constructs, got %d", len(catalog.Constructs))
	}
	for _, construct := range catalog.Constructs {
		if construct.Name == "expr" && len(construct.Variants) != 3 {
			t.Errorf("expression variants = %v", construct.Variants)
		}
		if construct.Name == "project" && len(construct.ChildRoles) == 0 {
			t.Error("project has no repeatable child roles")
		}
	}
	for _, field := range catalog.Fields {
		if field.Profile != "M0" || field.Layer != "semantic" {
			t.Errorf("invalid field gates for %s.%s", field.Construct, field.Name)
		}
		if field.Construct == "project" && field.Name == "edition" || field.Construct == "instrument" && field.Name == "octave" {
			expected := "1"
			if field.Construct == "instrument" {
				expected = "2"
			}
			if field.Required || field.Default == nil || *field.Default != expected {
				t.Errorf("legacy default is missing: %+v", field)
			}
			continue
		}
		if !field.Required {
			t.Errorf("unexpected optional semantic field %s.%s", field.Construct, field.Name)
		}
		if (field.Construct == "expr" || field.Construct == "value") && field.Name != "unit" && field.Variant == "" {
			t.Errorf("%s.%s lacks union variant", field.Construct, field.Name)
		}
		if field.Default != nil {
			t.Errorf("required semantic field %s.%s has a default", field.Construct, field.Name)
		}
		if field.Construct == "pattern" && field.Name == "steps" && (field.Range == nil || *field.Range != "1..64") {
			t.Errorf("pattern.steps range = %v", field.Range)
		}
	}
}
