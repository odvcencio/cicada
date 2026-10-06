package projectcopy

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/sampleasset"
	"m31labs.dev/cicada/host/takejournal"
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

func TestSaveAsVendorsLibrariesFromRetainedRevisionsAndCandidates(t *testing.T) {
	for _, kind := range []string{"revision", "candidate"} {
		t.Run(kind, func(t *testing.T) {
			root, user, _ := vendortest.Setup(t)
			score := filepath.Join(root, "main.cicada")
			retained, err := os.ReadFile(score)
			if err != nil {
				t.Fatal(err)
			}
			// Keep the original imported score only in retained state.
			current := []byte("cicada 2\ntrack lead acid {}\npattern melody { 1 . }\nscene verse { lead=melody }\nsong { verse }\n")
			vendortest.Write(t, root, "main.cicada", current)
			if kind == "revision" {
				prefix := ".cicada-studio-" + takejournal.Revision([]byte(score))[:16] + "-retained"
				vendortest.Write(t, root, prefix, retained)
				vendortest.Write(t, root, prefix+".revision", []byte(takejournal.Revision(retained)))
			} else {
				_, recorder, id := pending(t, root, true)
				if err := recorder.Publish(id); err != nil {
					t.Fatal(err)
				}
				if err := recorder.Prepare(id, []byte(copyScore), retained); err != nil {
					t.Fatal(err)
				}
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
			if err := os.WriteFile(target, retained, 0600); err != nil {
				t.Fatal(err)
			}
			restored, ds, err := project.LoadScore(target, nil)
			if err != nil || restored == nil || len(ds) != 0 {
				t.Fatalf("retained import cannot be restored: %v %+v", err, ds)
			}
		})
	}
}

func TestSaveAsRejectsDestinationInsideVendoredLibrary(t *testing.T) {
	root, _, _ := vendortest.Setup(t)
	// A legacy manifest does not impose a separate source-path restriction.
	vendortest.Write(t, root, "cicada.mod", []byte("project score\ncicada 2\n"))
	targetRoot := t.TempDir()
	vendortest.Write(t, targetRoot, "cicada.mod", []byte("project score\ncicada 2\n"))
	target := filepath.Join(targetRoot, "lib/demo/tone/copy.cicada")
	if err := SaveAs(filepath.Join(root, "main.cicada"), target); err == nil {
		t.Fatal("score was published inside a library")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("rejected destination was written")
	}
	if _, err := os.Stat(filepath.Join(targetRoot, "lib/demo/tone/cicada.mod")); !os.IsNotExist(err) {
		t.Fatal("dependency copied before rejecting destination")
	}
}

func TestSaveAsAcceptsSourceDirectoryAlias(t *testing.T) {
	root, user, _ := vendortest.Setup(t)
	vendortest.Write(t, root, "cicada.mod", []byte("project score\ncicada 2\n"))
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip(err)
	}
	target := filepath.Join(alias, "copy.cicada")
	if err := SaveAs(filepath.Join(root, "main.cicada"), target); err != nil {
		t.Fatalf("alias Save As: %v", err)
	}
	if err := os.RemoveAll(user); err != nil {
		t.Fatal(err)
	}
	if score, ds, err := project.LoadScore(target, nil); err != nil || score == nil || len(ds) != 0 {
		t.Fatalf("alias copy: %v %+v", err, ds)
	}
}
