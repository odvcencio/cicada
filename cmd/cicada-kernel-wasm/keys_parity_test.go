//go:build wasm_integration && keys

package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"

	"m31labs.dev/cicada/host/keyboard"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func TestAudioWASMKeysScoreParity(t *testing.T) {
	wasm, err := os.ReadFile(wasmModulePath())
	if err != nil {
		t.Fatal(err)
	}
	hashes := map[int][]byte{44100: nil, 48000: nil}
	for _, patch := range keyboard.Names {
		t.Run(patch, func(t *testing.T) {
			source := fmt.Sprintf("cicada 2\ntempo 240\ntrack keys %s { voices=4 }\npattern p notes { [c4 e4 g4 b4] . c5 . }\nscene dry { keys=p }\nscene pedal { keys=p keys.sustain=1 }\nsong { dry pedal }\n", patch)
			s, ds := notation.Parse([]byte(source))
			for _, d := range ds {
				if d.Severity == "error" {
					t.Fatalf("parse: %v", ds)
				}
			}
			p, ds := project.FromScore(s)
			if p == nil {
				t.Fatalf("project: %v", ds)
			}
			result := compareWASMProject(t, "keys-"+patch, p, 2, true, wasm)
			for rate, hash := range result {
				hashes[rate] = append(hashes[rate], hash[:]...)
			}
		})
	}
	expected := map[int]string{
		44100: "aa69ebe731df8712914e6ed476729ccbccdd6117a5584cb55faf5106ceb18605",
		48000: "c48ec5b7762994f6d9448b2bc8a74ea636540cf032288d99e96cad8561bbc5fd",
	}
	for _, rate := range []int{44100, 48000} {
		golden := sha256.Sum256(hashes[rate])
		if fmt.Sprintf("%x", golden) != expected[rate] {
			t.Fatalf("keys score golden at %d changed: %x", rate, golden)
		}
		t.Logf("keys_score_golden rate=%d sha256=%x patches=%d", rate, golden, len(keyboard.Names))
	}
}
