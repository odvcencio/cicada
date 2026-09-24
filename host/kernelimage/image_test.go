package kernelimage_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

func firstAcidConfig(t *testing.T) engine.Config {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "examples", "first-acid.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("first-acid parse: %+v", diagnostic)
		}
	}
	p, diagnostics := project.FromScore(score)
	if p == nil {
		t.Fatalf("first-acid project: %+v", diagnostics)
	}
	cfg, err := project.CompileEngine(p, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestFirstAcidProjectImageRoundTrip(t *testing.T) {
	cfg := firstAcidConfig(t)
	encoded, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > kernelimage.MaxImageBytes {
		t.Fatalf("image grew to %d bytes", len(encoded))
	}
	decoded, err := kernelimage.Decode(encoded, 48_000, 128)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("compiled project changed in binary image round trip")
	}
	if _, err := engine.New(decoded); err != nil {
		t.Fatalf("decoded first-acid project cannot play: %v", err)
	}
}

func TestProjectImageRejectsCorruption(t *testing.T) {
	encoded, err := kernelimage.Encode(firstAcidConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{0, 31, 32, len(encoded) - 1} {
		if _, err := kernelimage.Decode(encoded[:length], 48_000, 128); err == nil {
			t.Fatalf("truncated image of %d bytes was accepted", length)
		}
	}
	if _, err := kernelimage.Decode(encoded, 44_100, 128); err == nil {
		t.Fatal("sample-rate mismatch was accepted")
	}
	corrupt := append([]byte(nil), encoded...)
	corrupt[0] = 'X'
	if _, err := kernelimage.Decode(corrupt, 48_000, 128); err == nil {
		t.Fatal("bad magic was accepted")
	}
	corrupt = append(append([]byte(nil), encoded...), 0)
	if _, err := kernelimage.Decode(corrupt, 48_000, 128); err == nil {
		t.Fatal("trailing bytes were accepted")
	}
}
