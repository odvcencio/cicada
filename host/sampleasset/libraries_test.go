package sampleasset

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/project"
)

func TestLibrarySamplerAndStreamUseLibraryRoot(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	t.Setenv("CICADA_LIBRARY", user)
	data := testwav.Bytes(48000, 1, 16, 4800, 1)
	for filename, contents := range map[string][]byte{
		filepath.Join(root, "cicada.mod"):                  []byte("project score\ncicada 2\nentry \"main.cicada\"\n"),
		filepath.Join(root, "main.cicada"):                 []byte("import \"demo/samples\"\ntrack lead samples.hit {}\nscene verse { lead = samples.melody }\nsong { verse }\n"),
		filepath.Join(user, "demo/samples/cicada.mod"):     []byte("library demo/samples\ncicada 2\nsource \"samples.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n"),
		filepath.Join(user, "demo/samples/samples.cicada"): []byte(fmt.Sprintf("asset wave \"audio/tone.wav\" { sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nsampler hit { asset = wave root = c3 mode = oneshot voices = 8 }\npattern melody { c3 . c3 . }\n", sha256.Sum256(data))),
		filepath.Join(user, "demo/samples/audio/tone.wav"): data,
	} {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, contents, 0644); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := project.ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.sum"), sum, 0644); err != nil {
		t.Fatal(err)
	}
	score, ds := sources.Parse()
	if score == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(score)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	if _, err := LoadSampler(root, p, "demo.samples.hit"); err != nil {
		t.Fatalf("sampler library root: %v", err)
	}
	source, err := OpenStream(root, p.Assets[0])
	if err != nil {
		t.Fatalf("stream library root: %v", err)
	}
	source.Close()
	// Decoding also verifies the asset after loading; later disk changes cannot
	// silently change a prepared sampler or stream.
	if err := os.WriteFile(filepath.Join(user, "demo/samples/audio/tone.wav"), testwav.Bytes(48000, 1, 16, 4800, 2), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSampler(root, p, "demo.samples.hit"); err == nil {
		t.Fatal("changed sampler asset accepted")
	}
	if stream, err := OpenStream(root, p.Assets[0]); err == nil {
		stream.Close()
		t.Fatal("changed stream asset accepted")
	}
}
