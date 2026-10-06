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
	artifact, err := os.ReadFile("schema/cicada.fields-2.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(data, '\n'), artifact) {
		t.Fatal("field catalog changed; regenerate with go run ./cmd/cicada fields > project/schema/cicada.fields-2.json")
	}
	catalog, err := Fields()
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Format != "cicada.fields/2" || len(catalog.Fields) < 70 {
		t.Fatalf("incomplete field catalog: %s, %d fields", catalog.Format, len(catalog.Fields))
	}
	if len(catalog.Constructs) != 35 {
		t.Fatalf("expected 35 semantic constructs, got %d", len(catalog.Constructs))
	}
	arrangementConstructs := map[string]bool{"arrangement": false, "placement": false, "marker": false}
	for _, construct := range catalog.Constructs {
		if _, ok := arrangementConstructs[construct.Name]; ok {
			arrangementConstructs[construct.Name] = true
		}
		if construct.Name == "expr" && len(construct.Variants) != 3 {
			t.Errorf("expression variants = %v", construct.Variants)
		}
		if construct.Name == "project" && len(construct.ChildRoles) == 0 {
			t.Error("project has no repeatable child roles")
		}
		if construct.Name == "scene_setting" || construct.Name == "scene_value" {
			if construct.Introduced != "cicada.project/2" {
				t.Errorf("%s construct introduced = %q", construct.Name, construct.Introduced)
			}
		}
	}
	for name, present := range arrangementConstructs {
		if !present {
			t.Errorf("missing arrangement construct %s", name)
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
		if field.Construct == "step" && field.Name == "notes" {
			if field.Required || field.Default != nil {
				t.Errorf("chord payload must remain optional: %+v", field)
			}
			continue
		}
		if field.Construct == "scene" && field.Name == "settings" {
			if field.Required || field.Introduced != "cicada.project/2" {
				t.Errorf("scene settings field metadata is wrong: %+v", field)
			}
			continue
		}
		if field.Construct == "pattern" && field.Name == "step_ticks" {
			if field.Required || field.Range == nil || *field.Range != "0..3840" {
				t.Errorf("grid field metadata is wrong: %+v", field)
			}
			continue
		}
		if field.Introduced == "cicada.project/2" {
			continue
		}
		if field.Construct == "pattern" && field.Name == "expression" {
			if field.Required {
				t.Error("pattern expression must remain optional for existing projects")
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
