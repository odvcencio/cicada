package filetarget

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreationPathsResolveExistingAncestors(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	if err := os.Mkdir(physical, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink("physical", link); err != nil {
		t.Fatal(err)
	}
	for name, parent := range map[string]string{"existing": physical, "linked": link} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(parent, "missing", "nested", "score.cicada")
			want := filepath.Join(physical, "missing", "nested", "score.cicada")
			for _, resolve := range []func(string) (Target, error){Create, Write} {
				got, err := resolve(path)
				if err != nil || got.Logical != path || got.Physical != want {
					t.Errorf("creation target: %+v %v; want logical %q physical %q", got, err, path, want)
				}
			}
			if _, err := Resolve(path, Existing); !os.IsNotExist(err) {
				t.Errorf("existing target accepted: %v", err)
			}
			if _, err := os.Stat(filepath.Join(physical, "missing")); !os.IsNotExist(err) {
				t.Errorf("resolution created directories: %v", err)
			}
		})
	}
}

func TestExistingTargetKeepsLogicalIdentity(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "target.cicada")
	if err := os.WriteFile(physical, []byte("score"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, destination := range map[string]string{"absolute": physical, "relative": filepath.Base(physical)} {
		t.Run(name, func(t *testing.T) {
			logical := filepath.Join(root, name+".cicada")
			if err := os.Symlink(destination, logical); err != nil {
				t.Fatal(err)
			}
			got, err := Resolve(logical, Existing)
			if err != nil || got.Logical != logical || got.Physical != physical {
				t.Fatalf("target: %+v %v; want logical %q physical %q", got, err, logical, physical)
			}
		})
	}
}

func TestCreationPathsRefuseDanglingAncestors(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "alias")
	if err := os.Symlink("missing", link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(link, "nested", "score.cicada")} {
		if _, err := Write(path); err == nil {
			t.Errorf("dangling link accepted: %s", path)
		}
	}
	if got, err := os.Readlink(link); err != nil || got != "missing" {
		t.Fatalf("link changed: %q %v", got, err)
	}
}
