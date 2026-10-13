package main

import (
	"bytes"
	"strings"
	"testing"

	edits "m31labs.dev/cicada/edit"
	"m31labs.dev/cicada/host/padsampler"
	"m31labs.dev/cicada/project"
)

// originalPlanCompiler returns the real plan for the unedited score and a
// text-only plan for candidates, which reference a print that is not on disk.
type originalPlanCompiler struct {
	original []byte
	plan     *edits.Plan
}

func (c originalPlanCompiler) Compile(source []byte, _ map[string][]byte) (*edits.Plan, error) {
	if bytes.Equal(source, c.original) {
		return c.plan, nil
	}
	return &edits.Plan{Revision: edits.Revision(source)}, nil
}

// The recorded outcomes are what the retired placeSamplerPrint produced.
func TestSamplerPrintIntentWritesTheRecordedBytes(t *testing.T) {
	golden := m10Golden(t, "sampler")
	withAudio := strings.Replace(studioListeningScore, "pattern pulse", "track vox audio {}\npattern pulse", 1)
	withScene := strings.Replace(studioListeningScore, "track left", "scene main { left=pulse }\ntrack left", 1)
	crlf := strings.ReplaceAll(studioListeningScore, "\n", "\r\n")
	cases := []struct {
		name, source string
		input        samplerCommand
		frames       int64
	}{
		{"new track", studioListeningScore, samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 96000},
		{"existing audio track", withAudio, samplerCommand{NewName: "print", Track: "vox", At: "@5.1.1"}, 48000},
		{"new track before scene", withScene, samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 48000},
		{"crlf", crlf, samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 48000},
		{"rounds length up", studioListeningScore, samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 47999},
		{"note track", studioListeningScore, samplerCommand{NewName: "print", Track: "left", At: "@5.1.1"}, 48000},
		{"bad track name", studioListeningScore, samplerCommand{NewName: "print", Track: "Bad Track", At: "@5.1.1"}, 48000},
		{"taken placement name", studioListeningScore, samplerCommand{NewName: "bed", Track: "sampler", At: "@5.1.1"}, 48000},
		{"bad name", studioListeningScore, samplerCommand{NewName: "Bad Name", Track: "sampler", At: "@5.1.1"}, 48000},
		{"no start", studioListeningScore, samplerCommand{NewName: "print", Track: "sampler"}, 48000},
		{"bad start", studioListeningScore, samplerCommand{NewName: "print", Track: "sampler", At: "soon"}, 48000},
		{"scene song", strings.Replace(studioScore, "cicada 1", "cicada 2", 1), samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 48000},
		{"edition one", studioScore, samplerCommand{NewName: "print", Track: "sampler", At: "@5.1.1"}, 48000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := studioTestPath(t, c.source)
			p, err := compileStudioSource(path, []byte(c.source))
			if err != nil {
				t.Fatalf("fixture does not compile: %v", err)
			}
			src := padsampler.Source{Asset: project.Asset{Path: "sampler/audio/print.wav", SHA256: strings.Repeat("ab", 32), Frames: c.frames, RateHz: padsampler.Rate, Channels: 2}}
			compiler := originalPlanCompiler{original: []byte(c.source), plan: project.EditPlan(p, []byte(c.source))}
			got, err := edits.Apply([]byte(c.source), edits.Envelope{Version: edits.EnvelopeVersion, Intents: []edits.Intent{samplerPrintIntent(src, c.input)}}, edits.Options{Compiler: compiler, Path: path})
			var source []byte
			if err == nil {
				source = got.Source
			}
			m10Check(t, golden, c.name, source, err)
		})
	}
}
