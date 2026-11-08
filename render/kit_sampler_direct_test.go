package render

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/project"
)

// The direct mixer has no pack voices. A sampler kit lane that reached it would
// play a builtin sound in its place, so it is an explicit error.
func TestDirectMixerRefusesSamplerKitLane(t *testing.T) {
	const source = `cicada 2
tempo 120
sampler shells { pack = "packs/shells/manifest.json" sha256 = "0000000000000000000000000000000000000000000000000000000000000000" root = c4 voices = 4 }
kit k { sd = shells.d2 bd = builtin.bd }
track drumsk k {}
pattern p drums steps = 8 { sd: x... x... }
scene main { drumsk = p }
song { main }
`
	score, diagnostics := notation.Parse([]byte(source))
	if score == nil {
		t.Fatalf("parse: %+v", diagnostics)
	}
	semantic, diagnostics := project.FromScore(score)
	if semantic == nil {
		t.Fatalf("project: %+v", diagnostics)
	}
	_, err := compileTracksWithPacks(score, semantic, 48000, nil, false)
	if err == nil || !strings.Contains(err.Error(), "sampler kit lanes need the scheduled engine") {
		t.Fatalf("direct mixer accepted a sampler kit lane: %v", err)
	}
}
