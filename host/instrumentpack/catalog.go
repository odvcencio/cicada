package instrumentpack

import (
	"encoding/json"
	"fmt"
)

const DefaultTier = "hq16"

type CatalogPack struct {
	ID              string                 `json:"id"`
	Manifest        string                 `json:"manifest"`
	SHA256          string                 `json:"sha256"`
	CompressedBytes int64                  `json:"compressed_bytes"`
	Tiers           map[string]CatalogPack `json:"tiers,omitempty"`
}

// SelectCatalogPack defaults new downloads to scaled PCM16. Explicit gzip
// selection and legacy catalogs retain their original manifest and pin.
func SelectCatalogPack(data []byte, id, tier string) (CatalogPack, error) {
	var catalog struct {
		Packs []CatalogPack `json:"packs"`
	}
	if len(data) > MaxManifestBytes {
		return CatalogPack{}, fmt.Errorf("catalog exceeds limit")
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return CatalogPack{}, err
	}
	if tier == "" {
		tier = DefaultTier
	}
	if tier != "hq16" && tier != "lossless" && tier != "gzip" {
		return CatalogPack{}, fmt.Errorf("unknown pack tier")
	}
	for _, p := range catalog.Packs {
		if p.ID != id {
			continue
		}
		if tier != "gzip" && !(tier == DefaultTier && len(p.Tiers) == 0) {
			selected, ok := p.Tiers[tier]
			if !ok {
				return CatalogPack{}, fmt.Errorf("pack tier unavailable")
			}
			selected.ID = p.ID
			p = selected
		}
		if !ValidPath(p.ID) || !ValidPath(p.Manifest) || !validHash(p.SHA256) {
			return CatalogPack{}, fmt.Errorf("invalid catalog pack pin/path")
		}
		return p, nil
	}
	return CatalogPack{}, fmt.Errorf("unknown instrument pack")
}
