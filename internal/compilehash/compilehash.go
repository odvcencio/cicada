// Package compilehash hashes what Cicada compiles from a score and hands to a
// browser or the service: the engine configuration (as JSON) and the portable
// kernel image. They fall under the same contract as the audio, so the parity
// tests compare these hashes between amd64, arm64 and Go's own js/wasm build.
package compilehash

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/host/schedule"
	"m31labs.dev/cicada/project"
)

// Entry is one hash: the SHA-256 in hex, or "none" with the step that failed
// as Status (StatusLoad, StatusConfig, StatusImageCompile, StatusImageEncode).
type Entry struct {
	Sum    string
	Status int
}

// The step that failed, recorded so that a score that fails differently on
// two machines differs too.
const (
	StatusLoad         = 1
	StatusConfig       = 2
	StatusImageCompile = 3
	StatusImageEncode  = 4
)

func (e Entry) String() string { return fmt.Sprintf("%s exit=%d", e.Sum, e.Status) }

// Load reads a .cicada score as the cicada command does for a successful load:
// project.LoadScore, then project.FromScore.
func Load(path string) (*project.Project, error) {
	score, diagnostics, err := project.LoadScore(path, nil)
	if err != nil {
		return nil, err
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return nil, fmt.Errorf("%s: %s", path, d.Message)
		}
	}
	p, _ := project.FromScore(score)
	if p == nil {
		return nil, fmt.Errorf("%s: the score does not compile to a project", path)
	}
	return p, nil
}

// Score returns the hashes of the score at root/score (a slash-separated
// path): its engine configuration as project.CompileEngine builds it at
// 48 kHz, and its portable kernel image as schedule.CompileResident and
// kernelimage.Encode build it (`cicada check --profile browser`).
func Score(root, score string) (config, image Entry) {
	config, image = Entry{Sum: "none"}, Entry{Sum: "none"}
	path := filepath.Join(root, filepath.FromSlash(score))
	p, err := Load(path)
	if err != nil {
		config.Status, image.Status = StatusLoad, StatusLoad
		return config, image
	}
	if cfg, err := project.CompileEngine(p, 48_000, 512); err != nil {
		config.Status = StatusConfig
	} else if data, err := json.Marshal(cfg); err != nil {
		config.Status = StatusConfig
	} else {
		config.Sum = sum(data)
	}
	if portable, err := schedule.CompileResident(p, filepath.Dir(path), 48_000, 128); err != nil {
		image.Status = StatusImageCompile
	} else if data, err := kernelimage.Encode(portable); err != nil {
		image.Status = StatusImageEncode
	} else {
		image.Sum = sum(data)
	}
	return config, image
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}
