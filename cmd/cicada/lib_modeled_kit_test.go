package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestModeledKitChecksBesideImportsAndInLibraries(t *testing.T) {
	const band = "kit band_kit { bd = model.kick rs = model.cross_stick ch = model.hat_closed }\npattern beat drums steps=16 { bd: x... .... x... .... rs: .... x... .... x... ch: x.x. x.x. x.x. x.x. }\n"
	const manifest = "project house\ncicada 2\nentry \"main.cicada\"\n"
	use := func(kit, pattern string) string {
		return fmt.Sprintf("tempo 120\nseed 7\ntrack drums %s { level = -12dB }\nscene main { drums = %s }\nsong { main }\n", kit, pattern)
	}
	for _, test := range []struct {
		name  string
		files map[string]string
	}{
		{"standard import", map[string]string{
			"cicada.mod":  manifest,
			"main.cicada": "import \"std/fx\"\n" + band + use("band_kit", "beat"),
		}},
		{"library kit", map[string]string{
			"cicada.mod":                manifest,
			"main.cicada":               "import \"demo/band\"\n" + use("band.band_kit", "band.beat"),
			"lib/demo/band/cicada.mod":  "library demo/band\ncicada 2\nsource \"band.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
			"lib/demo/band/band.cicada": band,
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("CICADA_LIBRARY", t.TempDir())
			for name, text := range test.files {
				full := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(text), 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(root)
			main := filepath.Join(root, "main.cicada")
			var output, diagnostics bytes.Buffer
			if err := libCommand([]string{"update"}, &output); err != nil {
				t.Fatal(err)
			}
			output.Reset()
			if err := checkCommand([]string{main}, &output, &diagnostics); err != nil {
				t.Fatalf("check: %v\n%s", err, &diagnostics)
			}
			if p, err := loadProject(main); err != nil || p == nil {
				t.Fatalf("play gate: %v", err)
			}
		})
	}
}
