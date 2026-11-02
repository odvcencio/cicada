package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The extracted archive must retain displaced versions from both Studio's
// current per-user state directory and the older adjacent-file layout.
func bundledRecoveryFixture(t *testing.T) (string, []byte, []byte) {
	t.Helper()
	isolateState(t)
	dir := t.TempDir()
	score := filepath.Join(dir, "source.cicada")
	original := []byte(studioScore)
	if err := os.WriteFile(score, original, 0600); err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(original, []byte("Studio"), []byte("Changed"), 1)
	committed, preserved, err := studioWriteIfRevision(score, updated, 0600, studioRevision(original), nil)
	if err != nil || !committed || preserved == "" {
		t.Fatalf("create Studio recovery: %v %v %q", committed, err, preserved)
	}
	legacy := bytes.Replace(original, []byte("Studio"), []byte("Older"), 1)
	legacyName := filepath.Join(dir, strings.TrimSuffix(studioRecoveryPattern(score), "*")+"legacy")
	if err := os.WriteFile(legacyName, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyName+".revision", []byte(studioRevision(legacy)), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "song.zip")
	if err := bundleCommand([]string{score, archive}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "song.zip")
	if err := bundleCommand([]string{score, second}); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("unchanged recovery history produced different bundles: %v", err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	dest := t.TempDir()
	for _, file := range reader.File {
		if !filepath.IsLocal(file.Name) {
			t.Fatalf("unsafe bundle path %q", file.Name)
		}
		data, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dest, file.Name)), 0700); err != nil {
			t.Fatal(err)
		}
		output, err := os.Create(filepath.Join(dest, file.Name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.ReadFrom(data); err != nil {
			t.Fatal(err)
		}
		data.Close()
		output.Close()
	}
	return filepath.Join(dest, "main.cicada"), original, legacy
}

func TestBundleRestoresStudioRecoveryAcrossExtraction(t *testing.T) {
	score, original, legacy := bundledRecoveryFixture(t)
	if _, ok, err := studioRecoverySource(score, studioRevision(original)); err != nil || ok {
		t.Fatalf("unexpected automatic recovery before restore: %v %v", ok, err)
	}
	for i := 0; i < 2; i++ {
		if err := restoreBundleHistory(score); err != nil {
			t.Fatal(err)
		}
	}
	for _, expected := range [][]byte{original, legacy} {
		got, ok, err := studioRecoverySource(score, studioRevision(expected))
		if err != nil || !ok || !bytes.Equal(got, expected) {
			t.Fatalf("recovered version = %q, found %v, error %v", got, ok, err)
		}
	}
}

func TestBundleRestoresOlderArchiveRecoveryNames(t *testing.T) {
	score, original, _ := bundledRecoveryFixture(t)
	manifestPath := filepath.Join(filepath.Dir(score), "cicada-bundle.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest bundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Files {
		old := manifest.Files[i].Path
		if !strings.HasPrefix(old, ".cicada-bundle-history-") {
			continue
		}
		name := ".cicada-studio-0123456789abcdef-" + strings.TrimPrefix(old, ".cicada-bundle-history-")
		if err := os.Rename(filepath.Join(filepath.Dir(score), old), filepath.Join(filepath.Dir(score), name)); err != nil {
			t.Fatal(err)
		}
		manifest.Files[i].Path = name
	}
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := restoreBundleHistory(score); err != nil {
		t.Fatal(err)
	}
	got, ok, err := studioRecoverySource(score, studioRevision(original))
	if err != nil || !ok || !bytes.Equal(got, original) {
		t.Fatalf("older bundle recovery = %q, found %v, error %v", got, ok, err)
	}
}

func TestBundleRestoresRecoveryForEveryProjectSource(t *testing.T) {
	isolateState(t)
	root := t.TempDir()
	want := map[string][]byte{}
	for _, name := range []string{"cicada.mod", "main.cicada", "parts/patterns.cicada", "parts/voices.cicada"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "multifile", name))
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
		if name == "cicada.mod" {
			continue
		}
		want[name] = data
		updated := append(bytes.Clone(data), []byte("\n// saved change\n")...)
		ok, _, err := studioWriteIfRevision(file, updated, 0600, studioRevision(data), nil)
		if err != nil || !ok {
			t.Fatalf("save %s: %v %v", name, ok, err)
		}
	}
	archive := filepath.Join(t.TempDir(), "project.zip")
	if err := bundleCommand([]string{filepath.Join(root, "main.cicada"), archive}); err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "project.zip")
	if err := bundleCommand([]string{filepath.Join(root, "main.cicada"), second}); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("multi-file recovery bundle changed without edits: %v", err)
	}
	r, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	extracted := t.TempDir()
	for _, file := range r.File {
		if !filepath.IsLocal(file.Name) {
			t.Fatalf("unsafe bundle path %q", file.Name)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(extracted, file.Name)), 0700); err != nil {
			t.Fatal(err)
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		writer, err := os.Create(filepath.Join(extracted, file.Name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.ReadFrom(reader); err != nil {
			t.Fatal(err)
		}
		reader.Close()
		writer.Close()
	}
	if err := restoreBundleHistory(filepath.Join(extracted, "main.cicada")); err != nil {
		t.Fatal(err)
	}
	for name, expected := range want {
		got, ok, err := studioRecoverySource(filepath.Join(extracted, name), studioRevision(expected))
		if err != nil || !ok || !bytes.Equal(got, expected) {
			t.Errorf("recovered %s: found=%v error=%v", name, ok, err)
		}
	}
}

func TestBundleRecoveryRefusesChangedFilesAndCollisions(t *testing.T) {
	t.Run("source outside project", func(t *testing.T) {
		score, _, _ := bundledRecoveryFixture(t)
		manifestPath := filepath.Join(filepath.Dir(score), "cicada-bundle.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		var manifest bundleManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		for id := range manifest.RecoverySources {
			manifest.RecoverySources[id] = "../outside.cicada"
		}
		data, err = json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := restoreBundleHistory(score); err == nil {
			t.Fatal("accepted a recovery source outside the project")
		}
	})
	t.Run("tampered inventory file", func(t *testing.T) {
		score, _, _ := bundledRecoveryFixture(t)
		entries, err := os.ReadDir(filepath.Dir(score))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if bundleRecoveryName.MatchString(entry.Name()) && !strings.HasSuffix(entry.Name(), ".revision") {
				if err := os.WriteFile(filepath.Join(filepath.Dir(score), entry.Name()), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := restoreBundleHistory(score); err == nil {
					t.Fatal("accepted modified recovery file")
				}
				return
			}
		}
		t.Fatal("bundle did not contain recovery file")
	})
	t.Run("destination collision", func(t *testing.T) {
		score, _, _ := bundledRecoveryFixture(t)
		collision := filepath.Join(filepath.Dir(score), strings.TrimSuffix(studioRecoveryPattern(score), "*")+"legacy")
		if err := os.WriteFile(collision, []byte("unrelated"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := restoreBundleHistory(score); err == nil {
			t.Fatal("accepted conflicting recovery destination")
		}
		if got, err := os.ReadFile(collision); err != nil || string(got) != "unrelated" {
			t.Fatalf("collision changed: %q %v", got, err)
		}
	})
	t.Run("symlinked recovery source", func(t *testing.T) {
		score, _, _ := bundledRecoveryFixture(t)
		entries, err := os.ReadDir(filepath.Dir(score))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !bundleRecoveryName.MatchString(entry.Name()) || strings.HasSuffix(entry.Name(), ".revision") {
				continue
			}
			path := filepath.Join(filepath.Dir(score), entry.Name())
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := restoreBundleHistory(score); err == nil {
				t.Fatal("accepted a symlinked recovery source")
			}
			return
		}
		t.Fatal("bundle did not contain recovery file")
	})
}
