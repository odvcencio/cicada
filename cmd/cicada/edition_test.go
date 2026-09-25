package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectEditionAndNewScore(t *testing.T) {
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	if err := newCommand([]string{"night-circuit"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("night-circuit", "main.cicada")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(string(data), "cicada 1") {
		t.Fatal("new scores should use the manifest instead of a header")
	}
	if _, err := loadProject(path); err != nil {
		t.Fatalf("new score does not compile: %v", err)
	}
	if err := newCommand([]string{"night-circuit"}); err == nil {
		t.Fatal("new overwrote an existing project")
	}
}

func TestNearestManifestAndUnsupportedEdition(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "songs")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cicada.mod"), []byte("project root\ncicada 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	score := filepath.Join(child, "song.cicada")
	if edition, manifest, err := scoreEdition(score); err != nil || edition != 1 || manifest != filepath.Join(root, "cicada.mod") {
		t.Fatalf("parent lookup = %d, %q, %v", edition, manifest, err)
	}
	if err := os.WriteFile(filepath.Join(child, "cicada.mod"), []byte("project child\ncicada 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := scoreEdition(score); err == nil || !strings.Contains(err.Error(), "CICADA-VERSION") {
		t.Fatalf("unsupported nearest manifest: %v", err)
	}
}
