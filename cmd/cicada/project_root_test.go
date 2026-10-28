package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/testpack"
)

// subfolderProject writes the shape of the Bon Voyage audio project: the
// manifest names main.cicada as the entry and lists a score in a subfolder, and
// the pack sits under the project folder, not under the subfolder. It returns
// the project folder.
func subfolderProject(t *testing.T) (root string, pack testpack.Pack) {
	t.Helper()
	root = t.TempDir()
	pack = testpack.Write(t, root, testpack.Options{})
	files := map[string]string{
		"cicada.mod":         "project subfolder\ncicada 2\nentry \"main.cicada\"\nsource \"main.cicada\"\nsource \"songs/theme.cicada\"\n",
		"main.cicada":        "cicada 2\ntempo 120\n" + pack.Sampler("sound", 2) + "track lead sound {}\nscene main { lead = melody }\nsong { main }\n",
		"songs/theme.cicada": "pattern melody notes { c4 . c5 . }\n",
	}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, pack
}

// A sampler pack path is project-relative, as the field's description says, and
// every command that opens a pack resolves it the way it resolves audio assets:
// from the folder of cicada.mod, not from the folder of the score it was given.
func TestCheckResolvesPackPathsFromTheProjectFolder(t *testing.T) {
	root, _ := subfolderProject(t)
	score := filepath.Join(root, "songs", "theme.cicada")
	for _, profile := range [][]string{nil, {"--profile", "browser"}} {
		var stdout, stderr bytes.Buffer
		if err := checkCommand(append(append([]string{}, profile...), score), &stdout, &stderr); err != nil {
			t.Errorf("check %v of a score listed in cicada.mod: %v\n%s", profile, err, stderr.String())
		}
	}
	// The entry at the project folder agrees, so the project is not the problem.
	if err := checkPaths([]string{filepath.Join(root, "main.cicada")}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	// A pack that is missing from the project folder is still reported.
	if err := os.RemoveAll(filepath.Join(root, "packs")); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if err := checkPaths([]string{score}, &bytes.Buffer{}, &stderr); err == nil || !strings.Contains(stderr.String(), "CICADA-SAMPLER-PACK") {
		t.Fatalf("a missing pack passed: %v\n%s", err, stderr.String())
	}
}
