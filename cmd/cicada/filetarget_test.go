package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestScoreWritersPreserveSymlinkTargets(t *testing.T) {
	before, after := []byte("before\n"), []byte("after\n")
	writers := map[string]func(string) error{
		"revision-checked": func(path string) error { return writeFixedRevision(path, before, after, 0600, nil) },
		"fix":              func(path string) error { return writeFixedScore(path, after, 0600) },
		"fmt":              func(path string) error { return writeAtomic(path, after) },
		"convert-gen-pins": func(path string) error { return writeNewAtomic(path, after) },
		"studio-auxiliary": func(path string) error {
			return studioWriteAuxiliaryFile(studioAuxiliaryFile{Path: path, Before: before, After: after, Mode: 0600})
		},
	}
	for name, write := range writers {
		for _, relative := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/absolute", true: "/relative"}[relative], func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				dir := t.TempDir()
				target := filepath.Join(dir, "target.cicada")
				link := filepath.Join(dir, "link.cicada")
				if err := os.WriteFile(target, before, 0600); err != nil {
					t.Fatal(err)
				}
				linkTarget := target
				if relative {
					linkTarget = filepath.Base(target)
				}
				if err := os.Symlink(linkTarget, link); err != nil {
					t.Fatal(err)
				}
				if err := write(link); err != nil {
					t.Fatal(err)
				}
				if got, err := os.Readlink(link); err != nil || got != linkTarget {
					t.Errorf("link replaced: %q %v", got, err)
				}
				if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, after) {
					t.Errorf("target not edited: %q %v", got, err)
				}
				if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0600 {
					t.Errorf("target mode: %v %v", info, err)
				}
			})
		}
	}
}

func TestScoreWritersRefuseDanglingLinks(t *testing.T) {
	for name, write := range map[string]func(string) error{
		"fix":              func(path string) error { return writeFixedScore(path, []byte("after"), 0600) },
		"convert-gen-pins": func(path string) error { return writeNewAtomic(path, []byte("after")) },
		"studio-new-manifest": func(path string) error {
			return studioWriteAuxiliaryFile(studioAuxiliaryFile{Path: path, After: []byte("after"), Mode: 0600})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			link := filepath.Join(t.TempDir(), "link.cicada")
			if err := os.Symlink("missing.cicada", link); err != nil {
				t.Fatal(err)
			}
			if err := write(link); err == nil {
				t.Error("dangling link accepted")
			}
			if got, err := os.Readlink(link); err != nil || got != "missing.cicada" {
				t.Fatalf("link changed: %q %v", got, err)
			}
		})
	}
}

func TestFixSymlinkDiscoversTargetProject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.cicada")
	before := []byte("track bass acid {}\npattern p notes steps=1 { c }\nscene main { bass=p }\nsong { main }\n")
	manifest := filepath.Join(dir, "cicada.mod")
	for file, data := range map[string][]byte{path: before, manifest: []byte("project target\ncicada 1\n")} {
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "alias.cicada")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := fixCommand([]string{link}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(link); err != nil || got != path {
		t.Errorf("link changed: %q %v", got, err)
	}
	if got, _ := os.ReadFile(manifest); string(got) != "project target\ncicada 2\n" {
		t.Errorf("target manifest not upgraded: %q", got)
	}
	if got, _ := os.ReadFile(path); bytes.Equal(got, before) {
		t.Error("target score not migrated")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(link), "cicada.mod")); !os.IsNotExist(err) {
		t.Errorf("manifest written beside alias: %v", err)
	}
}

func TestScoreWritersCreateThroughDirectorySymlink(t *testing.T) {
	after := []byte("new source\n")
	for name, write := range map[string]func(string) error{
		"fix":              func(path string) error { return writeFixedScore(path, after, 0600) },
		"convert-gen-pins": func(path string) error { return writeNewAtomic(path, after) },
		"import-midi":      func(path string) error { return writeImportedScore(path, after) },
		"studio-new-manifest": func(path string) error {
			return studioWriteAuxiliaryFile(studioAuxiliaryFile{Path: path, After: after, Mode: 0600})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			link := filepath.Join(t.TempDir(), "project")
			if err := os.Symlink(dir, link); err != nil {
				t.Fatal(err)
			}
			if err := write(filepath.Join(link, "new.cicada")); err != nil {
				t.Fatal(err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, "new.cicada")); err != nil || !bytes.Equal(got, after) {
				t.Errorf("new target: %q %v", got, err)
			}
			if got, err := os.Readlink(link); err != nil || got != dir {
				t.Errorf("directory link changed: %q %v", got, err)
			}
		})
	}
}

func TestImportedScoreRefusesSymlinkDestinations(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{false: "dangling", true: "existing"}[exists], func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "target.cicada")
			before := []byte("keep target\n")
			if exists {
				if err := os.WriteFile(target, before, 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(dir, "link.cicada")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := writeImportedScore(link, []byte("replacement")); err == nil {
				t.Fatal("exclusive creation accepted a link")
			}
			if got, err := os.Readlink(link); err != nil || got != target {
				t.Fatalf("link changed: %q %v", got, err)
			}
			got, err := os.ReadFile(target)
			if exists && (err != nil || !bytes.Equal(got, before)) || !exists && !os.IsNotExist(err) {
				t.Fatalf("target changed: %q %v", got, err)
			}
		})
	}
}
