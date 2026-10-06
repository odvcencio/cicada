package instrumentpack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Download verifies a pinned catalog and admits the selected bank before
// publishing a new directory. Existing different or corrupt packs are refused.
func Download(ctx context.Context, client *http.Client, catalogURL, catalogPin, id, tier, destination string) (CatalogPack, error) {
	var zero CatalogPack
	base, err := url.Parse(catalogURL)
	if err != nil || base.Scheme != "https" && base.Scheme != "http" || base.Host == "" || base.User != nil || !validHash(catalogPin) {
		return zero, fmt.Errorf("invalid catalog URL or pin")
	}
	fetch := func(u *url.URL, pin string, limit int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("pack fetch failed: %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit || digest(data) != pin {
			return nil, fmt.Errorf("pack hash/size mismatch")
		}
		return data, nil
	}
	data, err := fetch(base, catalogPin, MaxManifestBytes)
	if err != nil {
		return zero, err
	}
	selected, err := SelectCatalogPack(data, id, tier)
	if err != nil {
		return zero, err
	}
	manifestURL := base.ResolveReference(&url.URL{Path: selected.Manifest})
	manifestBytes, err := fetch(manifestURL, selected.SHA256, MaxManifestBytes)
	if err != nil {
		return zero, err
	}
	m, err := DecodeManifest(manifestBytes)
	if err != nil {
		return zero, err
	}
	if m.ID != id {
		return zero, fmt.Errorf("catalog/manifest ID mismatch")
	}
	if _, err := os.Stat(destination); err == nil {
		p, err := Load(destination, "manifest.json", selected.SHA256)
		if err != nil || p.Manifest.ID != id {
			return zero, fmt.Errorf("refusing to replace different or corrupt pack")
		}
		return selected, nil
	} else if !os.IsNotExist(err) {
		return zero, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return zero, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(destination), ".pack-download-")
	if err != nil {
		return zero, err
	}
	defer os.RemoveAll(stage)
	for _, a := range m.Assets {
		u := manifestURL.ResolveReference(&url.URL{Path: a.Path})
		encoded, err := fetch(u, a.SHA256, a.Bytes)
		if err != nil {
			return zero, err
		}
		if _, err := DecodeAsset(a, encoded); err != nil {
			return zero, err
		}
		target := filepath.Join(stage, filepath.FromSlash(a.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return zero, err
		}
		if err := os.WriteFile(target, encoded, 0644); err != nil {
			return zero, err
		}
	}
	if err := os.WriteFile(filepath.Join(stage, "manifest.json"), manifestBytes, 0644); err != nil {
		return zero, err
	}
	if _, err := Load(stage, "manifest.json", selected.SHA256); err != nil {
		return zero, err
	}
	// Check again after network/admission work; never replace an existing pack.
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return zero, fmt.Errorf("pack destination already exists")
	}
	if err := os.Rename(stage, destination); err != nil {
		return zero, err
	}
	return selected, nil
}
