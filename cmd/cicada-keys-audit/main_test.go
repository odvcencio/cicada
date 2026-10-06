package main

import (
	"os"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
)

func TestPublishedKeysCatalogPinsOwnedManifestsAndRecipes(t *testing.T) {
	root := filepath.Join("..", "..", "assets", "keys")
	c, err := readCatalog(filepath.Join(root, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Packs) != 21 {
		t.Fatalf("got %d keyboard packs", len(c.Packs))
	}
	for _, e := range c.Packs {
		t.Run(e.ID, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, e.Manifest))
			if err != nil {
				t.Fatal(err)
			}
			if hash(data) != e.SHA256 {
				t.Fatal("published manifest pin does not match")
			}
			m, err := instrumentpack.DecodeManifest(data)
			if err != nil {
				t.Fatal(err)
			}
			if m.ID != "cicada-keys-"+e.ID || len(m.Assets) != e.Assets {
				t.Fatal("published identity changed")
			}
			recipe, err := os.ReadFile(filepath.Join(root, e.Recipe))
			if err != nil {
				t.Fatal(err)
			}
			if hash(recipe) != e.RecipeSHA256 {
				t.Fatal("published recipe pin does not match")
			}
			for _, a := range m.Assets {
				if a.License != "CC0-1.0" || a.SourceURL != "https://github.com/odvcencio/cicada" || a.Attribution != "" {
					t.Fatal("owned audio provenance changed")
				}
			}
		})
	}
}

func TestAuditRefusesChangedPinnedRecipeBeforeLoadingAudio(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "test"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "test", "render-recipe.json"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = verifyPack(d, root, packEntry{ID: "test", Recipe: "test/render-recipe.json", RecipeSHA256: hash([]byte("original"))})
	if err == nil || err.Error() != "recipe checksum mismatch" {
		t.Fatalf("changed recipe admitted: %v", err)
	}
}
