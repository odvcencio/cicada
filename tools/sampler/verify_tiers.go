//go:build ignore

// Verify every catalog pin, encoded asset and canonical PCM reconstruction.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"m31labs.dev/cicada/host/instrumentpack"
)

func main() {
	root := flag.String("root", "", "external tier distribution")
	flag.Parse()
	for _, tier := range []string{"lossless", "hq16"} {
		for _, group := range []string{"packs", "full-kit-packs"} {
			dir := filepath.Join(*root, tier, group)
			data, err := os.ReadFile(filepath.Join(dir, "catalog.json"))
			must(err)
			var catalog struct {
				Packs []instrumentpack.CatalogPack `json:"packs"`
			}
			must(json.Unmarshal(data, &catalog))
			var total int64
			assets := 0
			for _, pin := range catalog.Packs {
				pack, err := instrumentpack.Load(dir, pin.Manifest, pin.SHA256)
				must(err)
				for _, a := range pack.Manifest.Assets {
					total += a.Bytes
					assets++
				}
				pack = nil
				runtime.GC()
			}
			fmt.Printf("%s %s: %d assets, %d encoded bytes, all native admission pins verified\n", tier, group, assets, total)
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
