// cicada-keys-audit verifies the published catalog against rebuilt owned packs.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"m31labs.dev/cicada/host/instrumentpack"
)

type packEntry struct {
	ID              string `json:"id"`
	Manifest        string `json:"manifest"`
	SHA256          string `json:"sha256"`
	Recipe          string `json:"recipe"`
	RecipeSHA256    string `json:"recipe_sha256"`
	Assets          int    `json:"assets"`
	CompressedBytes int64  `json:"compressed_bytes"`
	PCMBytes        int64  `json:"pcm_bytes"`
	Roots           int    `json:"roots"`
	VelocityLayers  int    `json:"velocity_layers"`
	RoundRobins     int    `json:"round_robins"`
	ReleaseZones    int    `json:"release_zones"`
	LoopZones       int    `json:"loop_zones"`
	License         string `json:"license"`
}
type catalog struct {
	Format string      `json:"format"`
	Packs  []packEntry `json:"packs"`
}

func main() {
	root := flag.String("packs", "", "directory containing generated keyboard packs")
	mapPath := flag.String("catalog", "assets/keys/catalog.json", "published catalog")
	flag.Parse()
	if flag.NArg() != 0 || *root == "" {
		fmt.Fprintln(os.Stderr, "--packs is required; positional arguments are unsupported")
		os.Exit(2)
	}
	if err := audit(*root, *mapPath, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cicada-keys-audit:", err)
		os.Exit(1)
	}
}
func readCatalog(file string) (catalog, error) {
	var c catalog
	f, err := os.Open(file)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 2<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, fmt.Errorf("trailing catalog data")
	}
	if c.Format != "cicada.instrument-catalog/1" || len(c.Packs) == 0 {
		return c, fmt.Errorf("invalid catalog")
	}
	seen := map[string]bool{}
	for _, p := range c.Packs {
		if !instrumentpack.ValidPath(p.ID) || filepath.Base(p.ID) != p.ID || seen[p.ID] || !instrumentpack.ValidPath(p.Manifest) || !instrumentpack.ValidPath(p.Recipe) || p.Manifest != p.ID+"/manifest.json" || p.Recipe != p.ID+"/render-recipe.json" || !validHash(p.SHA256) || !validHash(p.RecipeSHA256) || p.License != "CC0-1.0" || p.Assets < 1 || p.Assets > 4096 || p.CompressedBytes < 1 || p.PCMBytes < 1 || p.PCMBytes > instrumentpack.MaxPCMBytes || p.Roots < 1 || p.VelocityLayers < 5 || p.RoundRobins < 2 || p.ReleaseZones < 0 || p.LoopZones < 0 {
			return c, fmt.Errorf("invalid catalog entry %q", p.ID)
		}
		seen[p.ID] = true
	}
	return c, nil
}
func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == hex.EncodeToString(b)
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func audit(root, file string, out io.Writer) error {
	c, err := readCatalog(file)
	if err != nil {
		return err
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	for _, entry := range c.Packs {
		if err = verifyPack(dir, root, entry); err != nil {
			return fmt.Errorf("%s: %w", entry.ID, err)
		}
		fmt.Fprintf(out, "%s: pinned manifest, recipe, all %d assets, coverage, loops and releases passed\n", entry.ID, entry.Assets)
		runtime.GC()
	}
	fmt.Fprintf(out, "verified %d owned keyboard packs\n", len(c.Packs))
	return nil
}
func verifyPack(dir *os.Root, root string, e packEntry) error {
	recipe, err := dir.ReadFile(e.Recipe)
	if err != nil {
		return err
	}
	if hash(recipe) != e.RecipeSHA256 {
		return fmt.Errorf("recipe checksum mismatch")
	}
	p, err := instrumentpack.LoadAtRate(root, e.Manifest, e.SHA256, 48000)
	if err != nil {
		return err
	}
	if p.Manifest.ID != "cicada-keys-"+e.ID || len(p.Manifest.Assets) != e.Assets {
		return fmt.Errorf("pack identity or asset count mismatch")
	}
	var packed, pcm int64
	for _, a := range p.Manifest.Assets {
		if a.License != e.License || a.SourceURL != "https://github.com/odvcencio/cicada" || a.Attribution != "" {
			return fmt.Errorf("unexpected provenance")
		}
		packed += a.Bytes
		pcm += int64(a.Frames) * int64(a.Channels) * 4
	}
	roots := map[int]bool{}
	layers := map[int]bool{}
	release, loops := 0, 0
	var coverage [128][128]bool
	for _, z := range p.Manifest.Zones {
		if z.Count != e.RoundRobins {
			return fmt.Errorf("round robin count mismatch")
		}
		if z.Release {
			release++
			continue
		}
		roots[z.Root] = true
		layers[z.Layer] = true
		if z.Loop {
			loops++
		}
		for key := z.KeyLow; key <= z.KeyHigh; key++ {
			for vel := z.VelocityLow; vel <= z.VelocityHigh; vel++ {
				coverage[key][vel] = true
			}
		}
	}
	if packed != e.CompressedBytes || pcm != e.PCMBytes || len(roots) != e.Roots || len(layers) != e.VelocityLayers || release != e.ReleaseZones || loops != e.LoopZones {
		return fmt.Errorf("catalog dimensions mismatch")
	}
	for key := 21; key <= 108; key++ {
		for vel := 1; vel <= 127; vel++ {
			if !coverage[key][vel] {
				return fmt.Errorf("unmapped note%d velocity%d", key, vel)
			}
		}
	}
	_, err = p.New(48000)
	return err
}
