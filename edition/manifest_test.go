package edition

import (
	"errors"
	"reflect"
	"testing"
)

func TestManifestMetadataAndLoadOrder(t *testing.T) {
	m, err := ParseProjectManifest([]byte("project score\ncicada 2\nsource \"parts/z.cicada\"\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/a.cicada\"\nlicense \"Apache-2.0\"\nauthor \"Cicada contributors\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.License != "Apache-2.0" || m.Author != "Cicada contributors" || !reflect.DeepEqual(m.SourcePaths(), []string{"main.cicada", "parts/a.cicada", "parts/z.cicada"}) {
		t.Fatalf("manifest: %+v", m)
	}
	if ed, err := ParseManifest([]byte("project score\ncicada 1\n")); err != nil || ed != 1 {
		t.Fatalf("legacy manifest: %d %v", ed, err)
	}
}

func TestManifestRejectsInvalidSources(t *testing.T) {
	for _, directive := range []string{
		`entry "../main.cicada"`, `entry "/main.cicada"`, `entry "parts/../main.cicada"`, `entry "C:\\main.cicada"`, `entry "parts/*.cicada"`, `entry "main.txt"`, `entry main.cicada`, `source "parts/main.cicada"`, `license "MIT OR Apache-2.0"`, `author unquoted`, `require "future"`,
	} {
		t.Run(directive, func(t *testing.T) {
			_, err := ParseProjectManifest([]byte("project score\ncicada 2\n" + directive + "\n"))
			var diagnostic *ManifestError
			if !errors.As(err, &diagnostic) || diagnostic.Line < 1 || diagnostic.Column != 1 {
				t.Fatalf("want typed positioned error, got %v", err)
			}
		})
	}
}
