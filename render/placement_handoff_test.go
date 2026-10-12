package render

import (
	"bytes"
	"testing"

	"m31labs.dev/cicada/notation"
)

func renderHandoffSource(t *testing.T, source string) []byte {
	t.Helper()
	score, diagnostics := notation.Parse([]byte(source))
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatalf("parse: %+v", diagnostic)
		}
	}
	var output bytes.Buffer
	if _, err := WAV(score, Options{SampleRate: 48_000, Bits: 32, Block: 128}, &output); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

const handoffDrums = `cicada 2
tempo 120
track kit drums {}
pattern beat drums {
  bd: X... .... x... ....
  sd: .... X... .... x...
  oh: .... .... .... ..x.
}
`

const handoffAcid = `cicada 2
tempo 120
track bass acid {}
pattern riff acid { 1 . 5 . 1 . 5 . 1 . 5 . 1 . . 5~ }
`

func requireSameRender(t *testing.T, a, b string, message string) {
	t.Helper()
	if !bytes.Equal(renderHandoffSource(t, a), renderHandoffSource(t, b)) {
		t.Fatal(message)
	}
}

func TestAdjacentDrumPlacementsHandOffLikeOnePlacement(t *testing.T) {
	one := handoffDrums + "arrange { place a kit beat { at = @1.1.1 length = 2bars } }\n"
	split := handoffDrums + "arrange {\n place a kit beat { at = @1.1.1 length = 1bar }\n place b kit beat { at = @2.1.1 length = 1bar }\n}\n"
	requireSameRender(t, one, split, "split drum placements differ from one placement")
}

func TestAdjacentAcidPlacementsKeepLastStepSlide(t *testing.T) {
	one := handoffAcid + "arrange { place a bass riff { at = @1.1.1 length = 2bars } }\n"
	split := handoffAcid + "arrange {\n place a bass riff { at = @1.1.1 length = 1bar }\n place b bass riff { at = @2.1.1 length = 1bar }\n}\n"
	requireSameRender(t, one, split, "split acid placements break the slide at the boundary")
}

func TestDifferentPatternHandoffMatchesSceneSong(t *testing.T) {
	base := handoffDrums + "pattern fill drums {\n  bd: x... x... x... x...\n  sd: .... .... .... xxxx\n  oh: x... .... .... ....\n}\n"
	arranged := base + "arrange {\n place a kit beat { at = @1.1.1 length = 1bar }\n place b kit fill { at = @2.1.1 length = 1bar }\n}\n"
	scenes := base + "scene one { kit = beat }\nscene two { kit = fill }\nsong { one two }\n"
	requireSameRender(t, arranged, scenes, "placement handoff differs from the equivalent scene song")
}

func TestPlacementReleaseBeforeGapStillChokesLikeSceneOff(t *testing.T) {
	arranged := handoffDrums + "arrange {\n place a kit beat { at = @1.1.1 length = 1bar }\n place b kit beat { at = @3.1.1 length = 1bar }\n}\n"
	scenes := handoffDrums + "scene on { kit = beat }\nscene gap { kit = off }\nsong { on gap on }\n"
	requireSameRender(t, arranged, scenes, "a release followed by a gap no longer matches a scene off")
}
