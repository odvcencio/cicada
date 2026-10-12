// Package rendercheck implements edit.RenderCheck by rendering a score to PCM24
// and hashing the bytes. It is impure by design (it renders audio), so the edit
// package's purity gate does not cover it.
package rendercheck

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/project"
	"m31labs.dev/cicada/render"
)

type check struct{ path string }

// New returns a render check for the entry score at path. Hash substitutes the
// supplied bytes for the entry file, so the project manifest and libraries
// apply as they do when the score is on disk. Callers cache by revision.
func New(path string) edits.RenderCheck {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return check{path: path}
}

func (c check) Hash(source []byte) (string, error) {
	score, ds, err := project.LoadScore(c.path, map[string][]byte{c.path: source})
	if err != nil {
		return "", err
	}
	for _, d := range ds {
		if d.Severity == "error" {
			return "", fmt.Errorf("%d:%d %s: %s", d.Position.Line, d.Position.Column, d.Code, d.Message)
		}
	}
	if score == nil {
		return "", fmt.Errorf("score did not load")
	}
	dir := filepath.Dir(c.path)
	dither := false
	sum := sha256.New()
	_, err = render.WAV(score, render.Options{SampleRate: 48000, Bits: 24, Dither: &dither, Block: 4096, AssetRoot: dir, AssetDir: dir}, sum)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
