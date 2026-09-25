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
	for _, field := range catalog.Fields {
		if (field.Construct == "expr" || field.Construct == "value") && field.Name != "unit" && field.Variant == "" {
			t.Errorf("%s.%s lacks union variant", field.Construct, field.Name)
		}
		if field.Construct == "pattern" && field.Name == "steps" && field.Range != "1..64" {
			t.Errorf("pattern.steps range = %q", field.Range)
		}
	}
}
