package rendercheck

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/internal/testpack"
)

// Studio's edit check renders the score. A project may list its scores in
// subfolders; the packs it names are found from the folder of cicada.mod, as
// for every other command, whichever listed score the check was given.
func TestRenderCheckResolvesPacksFromTheProjectFolder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pack := testpack.Write(t, root, testpack.Options{})
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
	for _, name := range []string{"main.cicada", "songs/theme.cicada"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		source := []byte(files[name])
		hash, err := New(path).Hash(source)
		if err != nil || len(hash) != 64 {
			t.Fatalf("render check of %s: %q, %v", name, hash, err)
		}
		// The identity names the pack's files too, so that a changed pack is
		// never answered from an older approval.
		if _, ok := Identity(path, source, nil); !ok {
			t.Errorf("the render inputs of %s cannot be identified, so no result is cached", name)
		}
	}
}
