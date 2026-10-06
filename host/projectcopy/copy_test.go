package projectcopy

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/capture"
	"m31labs.dev/cicada/host/takejournal"
	"m31labs.dev/cicada/project"
)

const copyScore = "cicada 2\ntrack vox audio {}\ntrack bass acid {}\npattern pulse acid steps=4 { 1 . 5 . }\nscene main { bass = pulse vox = off }\nsong { main }\n"

func pending(t *testing.T, dir string, finalize bool) (string, *takejournal.Store, string) {
	t.Helper()
	path := filepath.Join(dir, "main.cicada")
	if err := os.WriteFile(path, []byte(copyScore), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := takejournal.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	id, err := s.Begin("vox", "main", takejournal.Revision([]byte(copyScore)), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	block := capture.RecordedBlock{Timing: capture.Block{SampleRate: 48000, Frames: 4}}
	if err = s.Write(id, block, [][]float32{{0, 0.5, -0.5, 1}}); err != nil {
		t.Fatal(err)
	}
	if err = s.Flush(id); err != nil {
		t.Fatal(err)
	}
	if finalize {
		if err = s.Finalize(id, false); err != nil {
			t.Fatal(err)
		}
	}
	return path, s, id
}
func TestSaveAsPendingAndActiveTakeSnapshots(t *testing.T) {
	for _, finalize := range []bool{false, true} {
		t.Run(fmt.Sprint(finalize), func(t *testing.T) {
			path, s, id := pending(t, t.TempDir(), finalize)
			target := filepath.Join(t.TempDir(), "copy.cicada")
			if err := SaveAs(path, target); err != nil {
				t.Fatal(err)
			}
			// A later block must not enter the already copied snapshot.
			if !finalize {
				b := capture.RecordedBlock{RawFrame: 4, Timing: capture.Block{SampleRate: 48000, Frames: 2}}
				if err := s.Write(id, b, [][]float32{{-1, -1}}); err != nil {
					t.Fatal(err)
				}
			}
			s.Close()
			if err := os.RemoveAll(filepath.Dir(path)); err != nil {
				t.Fatal(err)
			}
			copied, err := takejournal.Open(target)
			if err != nil {
				t.Fatal(err)
			}
			defer copied.Close()
			if err = copied.Recover(); err != nil {
				t.Fatal(err)
			}
			take, _ := copied.Get(id)
			if take.Frames != 4 {
				t.Fatalf("copied snapshot frames=%d", take.Frames)
			}
		})
	}
}
func TestSaveAsAssetCollisionAndMissingFailBeforeScore(t *testing.T) {
	path, s, id := pending(t, t.TempDir(), true)
	if err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, _ := s.Get(id)
	for _, kind := range []string{"collision", "missing", "corrupt", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "copy.cicada")
			if kind == "collision" {
				asset := filepath.Join(dir, take.Asset.Path)
				os.MkdirAll(filepath.Dir(asset), 0700)
				os.WriteFile(asset, []byte("unrelated audio"), 0600)
			}
			sourceAsset := filepath.Join(filepath.Dir(path), take.Asset.Path)
			original, _ := os.ReadFile(sourceAsset)
			if kind == "missing" {
				os.Remove(sourceAsset)
				os.Remove(filepath.Join(filepath.Dir(path), takejournal.Namespace(path), id+".wav"))
			}
			if kind == "corrupt" {
				os.WriteFile(sourceAsset, []byte("corrupted"), 0600)
			}
			if kind == "symlink" {
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "audio")); err != nil {
					t.Skip(err)
				}
			}
			if err := SaveAs(path, target); err == nil {
				t.Fatal("invalid dependencies accepted")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("target score published before dependencies")
			}
			if kind == "collision" {
				data, _ := os.ReadFile(filepath.Join(dir, take.Asset.Path))
				if !bytes.Equal(data, []byte("unrelated audio")) {
					t.Fatal("collision overwrote audio")
				}
			}
			if kind == "missing" || kind == "corrupt" {
				os.WriteFile(sourceAsset, original, 0600)
				os.WriteFile(filepath.Join(filepath.Dir(path), takejournal.Namespace(path), id+".wav"), original, 0600)
			}
		})
	}
}
func TestSaveAsNestedScoreAndAssetReferences(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "cicada.mod"), []byte("project test\ncicada 2\n"), 0600)
	nested := filepath.Join(dir, "scores")
	os.Mkdir(nested, 0700)
	path, s, id := pending(t, nested, true)
	if err := s.Publish(id); err != nil {
		t.Fatal(err)
	}
	take, _ := s.Get(id)
	source, err := takejournal.SelectSource([]byte(copyScore), take)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "saved.cicada")
	if err = SaveAs(path, target); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	copied, _ := os.ReadFile(target)
	if !bytes.Equal(original, copied) {
		t.Fatal("Save As changed source references")
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(target), take.Asset.Path)); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(filepath.Dir(target), "cicada.mod")); err != nil {
		t.Fatal(err)
	}
	recovered, err := takejournal.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if err = recovered.Recover(); err != nil {
		t.Fatal(err)
	}
	if len(recovered.Takes()) != 1 {
		t.Fatal("nested score journal missing")
	}
}

func TestSaveAsScoreWithoutAssetsCopiesAndKeepsManifest(t *testing.T) {
	dir := t.TempDir()
	targetDir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	data := []byte("title \"draft\"\n")
	manifest := []byte("project source\ncicada 2\n")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "cicada.mod"), manifest, 0600)
	target := filepath.Join(targetDir, "copy.cicada")
	if err := SaveAs(path, target); err != nil {
		t.Fatal(err)
	}
	copied, _ := os.ReadFile(target)
	if !bytes.Equal(copied, data) {
		t.Fatal("source-only Save As changed draft")
	}
	copied, _ = os.ReadFile(filepath.Join(targetDir, "cicada.mod"))
	if !bytes.Equal(copied, manifest) {
		t.Fatal("manifest not copied")
	}
	kept := []byte("project keep\ncicada 2\n")
	os.WriteFile(filepath.Join(targetDir, "cicada.mod"), kept, 0600)
	if err := SaveAs(path, filepath.Join(targetDir, "second.cicada")); err != nil {
		t.Fatal(err)
	}
	copied, _ = os.ReadFile(filepath.Join(targetDir, "cicada.mod"))
	if !bytes.Equal(copied, kept) {
		t.Fatal("existing manifest overwritten")
	}
}

func TestSaveAsRefusesScoreDestinationCollisions(t *testing.T) {
	for _, kind := range []string{"audio-same-project", "audio-new-project", "existing", "identical", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			path, store, id := pending(t, t.TempDir(), true)
			if err := store.Publish(id); err != nil {
				t.Fatal(err)
			}
			take, _ := store.Get(id)
			audio, err := os.ReadFile(filepath.Join(filepath.Dir(path), take.Asset.Path))
			if err != nil {
				t.Fatal(err)
			}
			take.Asset.Path = "saved.cicada"
			source, err := takejournal.SelectSource([]byte(copyScore), take)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, source, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), take.Asset.Path), audio, 0600); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if kind == "audio-same-project" {
				dir = filepath.Dir(path)
			}
			target := filepath.Join(dir, "saved.cicada")
			if kind == "existing" || kind == "identical" {
				target = filepath.Join(dir, "occupied.cicada")
			}
			var existing []byte
			switch kind {
			case "audio-same-project":
				existing = audio
			case "existing":
				existing = []byte("unrelated file")
			case "identical":
				existing = source
			case "manifest":
				target = filepath.Join(dir, "cicada.mod")
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "cicada.mod"), []byte("project source\ncicada 2\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if existing != nil {
				if err := os.WriteFile(target, existing, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err = SaveAs(path, target)
			if err == nil || !strings.Contains(err.Error(), "Save As") {
				t.Errorf("destination collision must fail clearly: %v", err)
			}
			after, readErr := os.ReadFile(target)
			if existing != nil && (readErr != nil || !bytes.Equal(after, existing)) {
				t.Fatal("Save As changed the occupied destination")
			}
			if existing == nil && !os.IsNotExist(readErr) {
				t.Fatal("destination dependency collision was not rejected before installation")
			}
			original, _ := os.ReadFile(path)
			if !bytes.Equal(original, source) {
				t.Fatal("Save As changed the original score")
			}
		})
	}
}

type collisionReader struct {
	reader io.Reader
	create func()
}

func (r *collisionReader) Read(p []byte) (int, error) {
	if r.create != nil {
		r.create()
		r.create = nil
	}
	return r.reader.Read(p)
}

func TestScoreInstallDoesNotReplaceConcurrentDestination(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	original := []byte("concurrent file")
	reader := &collisionReader{reader: bytes.NewReader([]byte(copyScore)), create: func() {
		if err := root.WriteFile("saved.cicada", original, 0600); err != nil {
			t.Fatal(err)
		}
	}}
	if err := install(root, "saved.cicada", reader, -1, true); err == nil {
		t.Error("installation replaced a destination created during staging")
	}
	after, err := root.ReadFile("saved.cicada")
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("concurrent destination was overwritten")
	}
	names, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer names.Close()
	entries, err := names.Readdirnames(-1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files leaked: %v (%v)", entries, err)
	}
}

func TestSaveAsCopiesMultiFileProjectAndUpdatesEntry(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"cicada.mod":         "project score\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"parts/voice.cicada\"\nlicense \"MIT\"\nauthor \"Cicada contributors\"\n",
		"main.cicada":        "scene verse { bass = pulse }\nsong { verse }\n",
		"parts/voice.cicada": "// preserve bytes\ntrack bass acid {}\npattern pulse { 1 . 5 . }\n",
	}
	for name, data := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	target := filepath.Join(t.TempDir(), "copy.cicada")
	if err := SaveAs(filepath.Join(root, "main.cicada"), target); err != nil {
		t.Fatal(err)
	}
	set, err := project.ReadSources(target, nil)
	if err != nil || len(set.Files) != 2 || set.Manifest.Entry != "copy.cicada" {
		t.Fatalf("copied manifest: %+v %v", set, err)
	}
	if string(set.Files[0].Source) != files["main.cicada"] || string(set.Files[1].Source) != files["parts/voice.cicada"] {
		t.Fatal("Save As changed source bytes")
	}
	score, ds := set.Parse()
	if p, _ := project.FromScore(score); p == nil || len(ds) != 0 {
		t.Fatalf("copied project: %+v", ds)
	}
}
