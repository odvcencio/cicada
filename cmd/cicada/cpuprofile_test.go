package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCPUProfileFlag(t *testing.T) {
	defer func() { stopCPUProfile = func() {} }()
	path := filepath.Join(t.TempDir(), "render.prof")
	for _, test := range []struct {
		name string
		args []string
		want []string
	}{
		{"separate value", []string{"cicada", "render", "song.cicada", "-o", "song.wav", "--cpuprofile", path}, []string{"cicada", "render", "song.cicada", "-o", "song.wav"}},
		{"joined value", []string{"cicada", "check", "--cpuprofile=" + path, "song.cicada"}, []string{"cicada", "check", "song.cicada"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := startCPUProfileFlag(test.args)
			if err != nil {
				t.Fatal(err)
			}
			stopCPUProfile()
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("arguments %q, want %q", got, test.want)
			}
			if info, err := os.Stat(path); err != nil || info.Size() == 0 {
				t.Fatalf("profile not written: %v %v", info, err)
			}
		})
	}
	// Other commands, and commands without the flag, keep their arguments.
	for _, args := range [][]string{{"cicada", "fmt", "--cpuprofile", path}, {"cicada", "render", "song.cicada", "-o", "song.wav"}, {"cicada"}} {
		if got, err := startCPUProfileFlag(args); err != nil || !reflect.DeepEqual(got, args) {
			t.Fatalf("%q: got %q, %v", args, got, err)
		}
	}
	for _, args := range [][]string{{"cicada", "apply", "song.cicada", "intents.json", "--cpuprofile"}, {"cicada", "audition", "song.cicada", "--cpuprofile="}, {"cicada", "stems", "song.cicada", "--cpuprofile", filepath.Join(t.TempDir(), "missing", "x.prof")}} {
		if _, err := startCPUProfileFlag(args); err == nil {
			t.Fatalf("%q: want an error", args)
		}
	}
}
