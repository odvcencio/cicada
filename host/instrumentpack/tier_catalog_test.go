package instrumentpack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedTierCatalogsRetainMapsAndPCMDimensions(t *testing.T) {
	for _, root := range []string{"../../assets/sampler", "../../assets/sampler/full-kit"} {
		data, err := os.ReadFile(filepath.Join(root, "catalog.json"))
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Packs []CatalogPack `json:"packs"`
		}
		if err := json.Unmarshal(data, &catalog); err != nil {
			t.Fatal(err)
		}
		for _, pack := range catalog.Packs {
			legacyBytes, err := os.ReadFile(filepath.Join(root, pack.Manifest))
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := DecodeManifest(legacyBytes)
			if err != nil {
				t.Fatal(err)
			}
			for _, tier := range []string{"lossless", "hq16"} {
				selected, err := SelectCatalogPack(data, pack.ID, tier)
				if err != nil {
					t.Fatal(err)
				}
				b, err := os.ReadFile(filepath.Join(root, selected.Manifest))
				if err != nil {
					t.Fatal(err)
				}
				if digest(b) != selected.SHA256 {
					t.Fatal("tier pin mismatch", pack.ID, tier)
				}
				m, err := DecodeManifest(b)
				if err != nil {
					t.Fatal(err)
				}
				zones, _ := json.Marshal(m.Zones)
				originalZones, _ := json.Marshal(legacy.Zones)
				if string(zones) != string(originalZones) || m.Config != legacy.Config || len(m.Assets) != len(legacy.Assets) {
					t.Fatal("tier changed map/config", pack.ID, tier)
				}
				var total int64
				for i, a := range m.Assets {
					old := legacy.Assets[i]
					if a.Frames != old.Frames || a.Rate != old.Rate || a.Channels != old.Channels || a.SourceSHA256 != old.SourceSHA256 || a.License != old.License || a.Attribution != old.Attribution {
						t.Fatal("tier changed dimensions/provenance", pack.ID, tier)
					}
					if tier == "hq16" && a.Encoding != "flac" {
						t.Fatal("PCM16 must use FLAC")
					}
					total += a.Bytes
				}
				if total != selected.CompressedBytes {
					t.Fatal("tier byte total mismatch")
				}
			}
		}
	}
}
