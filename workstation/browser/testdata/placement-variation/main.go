// Write a portable, synthetic imported-performance fixture for interface tests.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"m31labs.dev/cicada/project"
)

func main() {
	if len(os.Args) != 2 {
		panic("expected a disposable output directory")
	}
	root, err := filepath.Abs(os.Args[1])
	check(err)
	check(os.MkdirAll(root, 0700))
	write := func(name, data string) { check(os.WriteFile(filepath.Join(root, name), []byte(data), 0600)) }
	check(project.NewLibrary("motif", filepath.Join(root, "lib/motif")))
	write("lib/motif/main.cicada", `instrument glass { voice poly { out=sine(pitch)*env(gate,300ms)*(0.1+pressure*0.05+timbre*0.05) } }
pattern parent performance length=3840ticks slot=7 {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch { gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7; }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
`)
	write("cicada.mod", "project placement-variation\ncicada 2\nentry \"main.cicada\"\nsource \"arrangement.cicada\"\n")
	write("main.cicada", "// cicada-placement-variation-test: disposable fixture\ncicada 2\ntitle \"One phrase, two directions\"\ntempo 120\nseed 42\nimport \"motif\"\ntrack keys motif.glass { level=-24dB }\n")
	write("arrangement.cicada", strings.ReplaceAll("// Keep this section note.\narrange {\n  place first keys motif.parent { at=@1.1.1 length=1bar } // keep the original\n  place return keys motif.parent { at=@2.1.1 length=1bar } // vary this use\n}\n", "\n", "\r\n"))
	score := filepath.Join(root, "main.cicada")
	sources, err := project.ReadSources(score, nil)
	check(err)
	pins, _, err := sources.UpdateLibraries("")
	check(err)
	check(os.WriteFile(filepath.Join(root, "cicada.sum"), pins, 0600))
	check(json.NewEncoder(os.Stdout).Encode(map[string]string{"score": score, "arrangement": filepath.Join(root, "arrangement.cicada"), "library": filepath.Join(root, "lib/motif/main.cicada")}))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
