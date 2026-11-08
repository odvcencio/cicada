package instrumentpack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testpack"
)

// A pack reached through a symlink that leaves the project is refused with a
// message that names the cause and the fix, not the raw openat error.
func TestOpenExplainsAPackSymlinkedFromOutsideTheProject(t *testing.T) {
	shared, project := t.TempDir(), t.TempDir()
	pack := testpack.Write(t, shared, testpack.Options{})
	packDir := filepath.Dir(filepath.Join(shared, pack.Manifest))
	if err := os.MkdirAll(filepath.Join(project, "packs"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "packs", filepath.Base(packDir))
	if err := os.Symlink(packDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := instrumentpack.Open(project, filepath.ToSlash(filepath.Join("packs", filepath.Base(packDir), "manifest.json")), pack.SHA256)
	if err == nil || !strings.Contains(err.Error(), "leads outside the project folder through a symlink") || !strings.Contains(err.Error(), "hard-link") {
		t.Fatalf("want an outside-the-project explanation, got %v", err)
	}
	// A pack inside the project still opens.
	inside := testpack.Write(t, project, testpack.Options{ID: "inside"})
	if _, err := instrumentpack.Open(project, inside.Manifest, inside.SHA256); err != nil {
		t.Fatalf("pack inside the project: %v", err)
	}
}
