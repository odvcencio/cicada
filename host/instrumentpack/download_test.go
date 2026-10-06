package instrumentpack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadSelectsPCM16AndPreservesExistingPacks(t *testing.T) {
	m, manifest, encoded := encodedFixture(t, "integer.flac", 1.0/(1<<20))
	entry := CatalogPack{ID: m.ID, Manifest: "fixture/manifest.json", SHA256: digest(manifest)}
	entry.Tiers = map[string]CatalogPack{"hq16": entry, "lossless": entry}
	data, _ := json.Marshal(map[string]any{"packs": []CatalogPack{entry}})
	files := map[string][]byte{"/catalog.json": data, "/fixture/manifest.json": manifest, "/fixture/" + m.Assets[0].Path: encoded}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "pack")
	download := func() error {
		_, err := Download(context.Background(), server.Client(), server.URL+"/catalog.json", digest(data), m.ID, "", dest)
		return err
	}
	if err := download(); err != nil {
		t.Fatal(err)
	}
	if err := download(); err != nil {
		t.Fatal("identical offline destination refused", err)
	}
	if err := os.WriteFile(filepath.Join(dest, m.Assets[0].Path), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := download(); err == nil {
		t.Fatal("corrupt destination replaced")
	}
	b, _ := os.ReadFile(filepath.Join(dest, m.Assets[0].Path))
	if string(b) != "corrupt" {
		t.Fatal("destination mutated")
	}
	for _, tier := range []string{"hq16", "lossless", "gzip"} {
		if _, err := SelectCatalogPack(data, m.ID, tier); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := SelectCatalogPack(data, m.ID, "opus"); err == nil {
		t.Fatal("unsupported tier accepted")
	}
}
