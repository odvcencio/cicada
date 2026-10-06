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

func TestLibraryManifestMetadataAndRequirements(t *testing.T) {
	valid := "library demo/tone\ncicada 2\nsource \"z.cicada\"\nsource \"a.cicada\"\nlicense MIT\nauthor \"Cicada contributors\"\n"
	m, err := ParseProjectManifest([]byte(valid))
	if err != nil || m.Library != "demo/tone" || m.Project != "" || m.EngineEdition != 2 || !reflect.DeepEqual(m.SourcePaths(), []string{"a.cicada", "z.cicada"}) {
		t.Fatalf("library manifest: %+v %v", m, err)
	}
	m, err = ParseProjectManifest([]byte(valid + "engine 1\ncapabilities 0x4\n"))
	if err != nil || m.EngineEdition != 1 || m.Capabilities != 4 {
		t.Fatalf("requirements: %+v %v", m, err)
	}
	for _, invalid := range []string{
		valid + "project score\n", valid + "entry \"a.cicada\"\n", valid + "engine 0\n", valid + "capabilities -1\n", valid + "require demo/tone 1.0\n",
		"library ../tone\ncicada 2\nsource \"a.cicada\"\nlicense MIT\nauthor \"Cicada contributors\"\n",
		"library demo/tone\ncicada 2\n", "project score\ncicada 2\nengine 2\n",
	} {
		var diagnostic *ManifestError
		_, err := ParseProjectManifest([]byte(invalid))
		if !errors.As(err, &diagnostic) {
			t.Fatalf("expected manifest diagnostic, got %v", err)
		}
	}
}
