package main

import (
	"bytes"
	"fmt"
)

// Standalone transforms need the edition resolved from the project. Supply it
// only during the transform so authored headers, offsets, and saved bytes keep
// their original conventions.
// This remains only for the unported M4 project and M5 recording callers.
func (s *studio) sourceTransform(change func([]byte) ([]byte, error)) func([]byte) ([]byte, error) {
	return func(source []byte) ([]byte, error) {
		score, diagnostics, err := parseScoreForPath(s.path, source)
		if err != nil {
			return nil, err
		}
		if score == nil || hasDiagnosticErrors(diagnostics) {
			return nil, fmt.Errorf("score must validate before editing")
		}
		prepared, prefix, err := studioTransformSource(source, score.Version)
		if err != nil {
			return nil, err
		}
		updated, err := change(prepared)
		if err != nil {
			return nil, err
		}
		if len(prefix) > 0 {
			if !bytes.HasPrefix(updated, prefix) {
				return nil, fmt.Errorf("edited source no longer matches its inherited edition")
			}
			updated = bytes.Clone(updated[len(prefix):])
		}
		return updated, nil
	}
}
