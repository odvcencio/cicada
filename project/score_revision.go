package project

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// ScoreRevision identifies the captured score files, manifest and imported
// library definitions. It excludes sample payloads and does not certify asset
// integrity or validate a score for rendering.
func (s *Sources) ScoreRevision() string {
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	// These fields contain JSON-supported primitives; the hash writer cannot
	// fail. JSON boundaries and sorted map keys keep the snapshot deterministic.
	_ = encoder.Encode(s.Manifest)
	for _, file := range s.Files {
		_ = encoder.Encode(struct {
			Path, Library, Root string
			Edition             int
			Source              []byte
		}{file.Path, file.Library, file.Root, file.Edition, file.Source})
	}
	for _, name := range s.LibraryOrder {
		library := s.Libraries[name]
		_ = encoder.Encode(struct {
			Path, Kind string
			Manifest   []byte
		}{name, library.Kind, library.ManifestSource})
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

// ReadScoreRevision compares saved source definitions without loading sample
// payloads. Library resolution retains its normal ambiguity checks. The partial
// source set stays private so callers cannot use it to bypass render validation.
func ReadScoreRevision(path string) (string, error) {
	return ReadScoreRevisionWithOverrides(path, nil)
}

// ReadScoreRevisionWithOverrides identifies a candidate's exact source files
// before publication. It uses the same absolute-path overrides as ReadSources
// and does not open sample payloads or validate a render.
func ReadScoreRevisionWithOverrides(path string, overrides map[string][]byte) (string, error) {
	sources, err := readSourcesMode(path, overrides, nil, true)
	if err != nil {
		return "", err
	}
	return sources.ScoreRevision(), nil
}
