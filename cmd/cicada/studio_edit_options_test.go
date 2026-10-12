package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestStudioEditOptionsLoadCurrentProjectSources(t *testing.T) {
	root := t.TempDir()
	path, part, manifest := filepath.Join(root, "main.cicada"), filepath.Join(root, "part.cicada"), filepath.Join(root, "cicada.mod")
	for file, source := range map[string]string{
		path:     studioScore,
		part:     "scene alternate { drums=beat }\n",
		manifest: "project sources\ncicada 1\nentry \"main.cicada\"\nsource \"part.cicada\"\n",
	} {
		if err := os.WriteFile(file, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	current := []byte("// current entry override\n" + studioScore)
	for _, auxiliary := range []bool{false, true} {
		t.Run(map[bool]string{false: "disk auxiliaries", true: "candidate auxiliaries"}[auxiliary], func(t *testing.T) {
			files := map[string][]byte{}
			wantPart, wantEdition := []byte("scene alternate { drums=beat }\n"), 1
			if auxiliary {
				wantPart = []byte("// candidate secondary source\nscene alternate { drums=beat }\n")
				files[part] = bytes.Clone(wantPart)
				files[manifest] = []byte("project sources\ncicada 2\nentry \"main.cicada\"\nsource \"part.cicada\"\n")
				wantEdition = 2
			}
			s := &studio{path: path}
			opts, err := s.editOptions(current, files)
			if err != nil {
				t.Fatal(err)
			}
			if len(opts.Sources) != 2 || opts.Edition != wantEdition || opts.Path != path {
				t.Fatalf("project options: %+v", opts)
			}
			for _, source := range opts.Sources {
				want := current
				if source.Path == part {
					want = wantPart
				} else if source.Path != path {
					t.Fatalf("unexpected source %s", source.Path)
				}
				if !bytes.Equal(source.Source, want) {
					t.Fatalf("source %s does not use current overrides: %q", source.Path, source.Source)
				}
			}
			if _, mutated := files[path]; mutated {
				t.Fatal("loading options changed the caller's auxiliary overrides")
			}
		})
	}
}
