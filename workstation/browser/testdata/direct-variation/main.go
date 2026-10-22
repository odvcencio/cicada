// Write a pinned library of unused musical ideas for disposable UI tests.
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
		panic("expected a new disposable output directory")
	}
	root, err := filepath.Abs(os.Args[1])
	check(err)
	check(os.MkdirAll(root, 0700))
	check(project.NewLibrary("motif", filepath.Join(root, "lib", "motif")))
	write := func(name, data string) { check(os.WriteFile(filepath.Join(root, name), []byte(data), 0600)) }
	write("main.cicada", `// cicada-direct-variation-test: disposable fixture
cicada 2
title "An idea, a different direction"
tempo 120
seed 42
key c minor
import "motif"
track keys motif.glass { level=-24dB }
track upper motif.glass { octave=5 level=-18dB }
track drums drums { level=-24dB }
pattern foundation notes { c3 . g3 . }
scene main { keys=foundation upper=off drums=off }
song { main*4 }
`)
	write("lib/motif/main.cicada", strings.ReplaceAll(`instrument glass { octave=3 voice poly { out=sine(pitch)*env(gate,150ms)*(0.1+pressure*0.05+timbre*0.05) } }
phrase hook { [1 3 5]^ - . . }
pattern idea notes slot=7 { step=1/8t swing=54% gate=70% transpose=5 seed=99 use hook }
pattern played performance length=3840ticks slot=6 {
 gesture-group touch {
  gesture at=0ticks bend=0ct pressure=0 timbre=0.5;
  gesture at=960ticks bend=200ct pressure=0.8 timbre=0.9;
 }
 note 60 at=17ticks length=703ticks velocity=103 group=touch {
  gesture at=0ticks bend=25ct pressure=0.2 timbre=0.7;
 }
 note 64 at=19ticks length=1301ticks velocity=87 group=touch;
}
pattern beat drums slot=5 { bd: X.x.; sd: ..x.; }
`, "\n", "\r\n"))
	score := filepath.Join(root, "main.cicada")
	sources, err := project.ReadSources(score, nil)
	check(err)
	pins, _, err := sources.UpdateLibraries("")
	check(err)
	check(os.WriteFile(filepath.Join(root, "cicada.sum"), pins, 0600))
	check(json.NewEncoder(os.Stdout).Encode(map[string]string{"score": score, "library": filepath.Join(root, "lib", "motif", "main.cicada"), "pins": filepath.Join(root, "cicada.sum")}))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
