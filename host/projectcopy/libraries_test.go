package projectcopy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/internal/vendortest"
	"m31labs.dev/cicada/kernel/voice/sample"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

func TestSaveAsVendorsUserAndProjectLibraries(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(map[bool]string{false: "user", true: "project"}[local], func(t *testing.T) {
			root, user, sources := vendortest.Setup(t)
			if local {
				if _, err := sources.VendorLibraries(root); err != nil {
					t.Fatal(err)
				}
			}
			score := filepath.Join(root, "main.cicada")
			before, ds, err := project.LoadScore(score, nil)
			if err != nil || len(ds) != 0 {
				t.Fatalf("original: %v %+v", err, ds)
			}
			var original bytes.Buffer
			if _, err := render.WAV(before, render.Options{SampleRate: 48000, Bits: 32, Bars: 2}, &original); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), "copy.cicada")
			if err := SaveAs(score, target); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(user); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			after, ds, err := project.LoadScore(target, nil)
			if err != nil || len(ds) != 0 {
				t.Fatalf("copied: %v %+v", err, ds)
			}
			var copied bytes.Buffer
			if _, err := render.WAV(after, render.Options{SampleRate: 48000, Bits: 32, Bars: 2}, &copied); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original.Bytes(), copied.Bytes()) {
				t.Fatal("Save As changed render bytes")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(target), "lib", "std")); !os.IsNotExist(err) {
				t.Fatal("vendored embedded libraries")
			}
		})
	}
}

func TestSaveAsLibrarySamplerAssetsRenderAfterRemoval(t *testing.T) {
	root, user, _ := vendortest.Setup(t)
	audio := testwav.Bytes(48000, 1, 16, 4800, 1)
	vendortest.Write(t, user, "demo/tone/audio/nested/tone.wav", audio)
	declarations := fmt.Sprintf("asset wave \"audio/nested/tone.wav\" { sha256 = \"%x\" format = wav frames = 4800 rate = 48000Hz channels = 1 }\nsampler hit { asset = wave root = c3 mode = oneshot voices = 8 }\npattern melody { c3 . c3 . }\n", sha256.Sum256(audio))
	vendortest.Write(t, user, "demo/tone/parts/tone.cicada", []byte(declarations))
	vendortest.Write(t, root, "main.cicada", []byte("import \"demo/tone\"\ntrack lead tone.hit {}\nscene verse { lead = tone.melody }\nsong { verse*2 }\n"))
	sources, err := project.ReadSources(filepath.Join(root, "main.cicada"), nil)
	if err != nil {
		t.Fatal(err)
	}
	sum, _, err := sources.UpdateLibraries("")
	if err != nil {
		t.Fatal(err)
	}
	vendortest.Write(t, root, "cicada.sum", sum)
	before, ds := sources.Parse()
	if before == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	p, ds := project.FromScore(before)
	if p == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	original, err := sampleasset.LoadRegion(root, p.Assets[0], 0, 0, 48, false)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "copy.cicada")
	if err := SaveAs(filepath.Join(root, "main.cicada"), target); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(user); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	after, ds, err := project.LoadScore(target, nil)
	if err != nil || after == nil || len(ds) != 0 {
		t.Fatalf("copied assets: %v %+v", err, ds)
	}
	q, ds := project.FromScore(after)
	if q == nil || len(ds) != 0 {
		t.Fatal(ds)
	}
	copied, err := sampleasset.LoadRegion(filepath.Dir(target), q.Assets[0], 0, 0, 48, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sampleasset.LoadSampler(filepath.Dir(target), q, "demo.tone.hit"); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000} {
		a, err := sample.New(rate, original)
		if err != nil {
			t.Fatal(err)
		}
		b, err := sample.New(rate, copied)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.NoteOn(48, 127); err != nil {
			t.Fatal(err)
		}
		if err := b.NoteOn(48, 127); err != nil {
			t.Fatal(err)
		}
		for frame := 0; frame < rate/5; frame++ {
			al, ar := a.NextStereo()
			bl, br := b.NextStereo()
			if al != bl || ar != br {
				t.Fatalf("sampler copy changed PCM at rate=%d frame=%d", rate, frame)
			}
		}
		t.Logf("METRIC save_as_library_sampler rate=%d sample_voice_pcm=byte-identical source_project=deleted user_library=deleted", rate)
	}
}

func TestSaveAsLibraryFailuresDoNotPublishScore(t *testing.T) {
	for _, kind := range []string{"collision", "hash-mismatch", "sum-collision"} {
		t.Run(kind, func(t *testing.T) {
			root, user, _ := vendortest.Setup(t)
			targetRoot := t.TempDir()
			target := filepath.Join(targetRoot, "copy.cicada")
			sourceSum, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			switch kind {
			case "collision":
				vendortest.Write(t, targetRoot, "lib/demo/tone/cicada.mod", []byte("different library"))
			case "hash-mismatch":
				vendortest.Write(t, user, "demo/tone/parts/tone.cicada", []byte("instrument other { voice mono { out = sine(pitch) } }\n"))
			case "sum-collision":
				vendortest.Write(t, targetRoot, "cicada.sum", sourceSum)
			}
			if err := SaveAs(filepath.Join(root, "main.cicada"), target); err == nil {
				t.Fatal("invalid Save As succeeded")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("published target before verifying dependencies")
			}
			after, _ := os.ReadFile(filepath.Join(root, "cicada.sum"))
			if !bytes.Equal(sourceSum, after) {
				t.Fatal("Save As changed source pins")
			}
			if kind == "sum-collision" {
				after, _ := os.ReadFile(filepath.Join(targetRoot, "cicada.sum"))
				if !bytes.Equal(sourceSum, after) {
					t.Fatal("Save As changed conflicting target pins")
				}
			}
		})
	}
}
