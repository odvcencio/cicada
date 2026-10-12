package edit

import (
	"errors"
	"strings"
	"testing"
)

const templateHead = "title \"t\"\ntempo 130\nkey a minor\n\ntrack bass acid {}\ntrack drums drums {}\n\npattern pulse acid {\n  1 . . . 5 . . . 1 . . . 7 . . .\n}\n\npattern beat drums {\n  bd: X... .... X... ....\n}\n\n"

const templateScenes = "scene main { bass = pulse drums = beat }\nscene break { bass = pulse drums = beat }\n\n"

func place(name, track, content, at, length string) string {
	return "  place " + name + " " + track + " " + content + " {\n    at = " + at + " length = " + length + "\n  }\n"
}

func convertScore(t *testing.T, source string, opts Options) (*Result, error) {
	t.Helper()
	if opts.RenderCheck == nil {
		opts.RenderCheck = &fakeCheck{}
	}
	return applyArrange([]byte(source), `{"kind":"convertsongtoarrange"}`, opts)
}

func TestConvertSongToArrangeTemplate(t *testing.T) {
	song := templateHead + templateScenes + "song { main*4 break*4 }\n"
	arrange := "arrange {\n" + place("main-1-bass", "bass", "pulse", "@1.1.1", "4bars") + place("main-1-drums", "drums", "beat", "@1.1.1", "4bars") +
		place("break-2-bass", "bass", "pulse", "@5.1.1", "4bars") + place("break-2-drums", "drums", "beat", "@5.1.1", "4bars") + "}\n"
	want := "cicada 2\n" + templateHead + templateScenes + arrange
	got, err := convertScore(t, song, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Source) != want || got.Label != "Song converted to arrange" {
		t.Fatalf("got:\n%s\nwant:\n%s\nlabel %q", got.Source, want, got.Label)
	}
	// CRLF sources keep their line ending.
	crlf, err := convertScore(t, strings.ReplaceAll(song, "\n", "\r\n"), Options{})
	if err != nil || string(crlf.Source) != strings.ReplaceAll(want, "\n", "\r\n") {
		t.Fatalf("crlf: %q, %v", crlf.Source, err)
	}
}

func TestConvertSongToArrangeEditionHeader(t *testing.T) {
	song := templateHead + templateScenes + "song { main*1 }\n"
	// An existing edition-2 header is left alone.
	got, err := convertScore(t, "cicada 2\n"+song, Options{})
	if err != nil || strings.Count(string(got.Source), "cicada 2") != 1 || !strings.HasPrefix(string(got.Source), "cicada 2\ntitle") {
		t.Fatalf("header 2: %v", err)
	}
	// An edition-1 header becomes 2.
	got, err = convertScore(t, "cicada 1\n"+song, Options{})
	if err != nil || !strings.HasPrefix(string(got.Source), "cicada 2\ntitle") {
		t.Fatalf("header 1: %v", err)
	}
	// A manifest at edition 2 supplies the edition; no header is added.
	manifest := Options{Edition: 2, ManifestPath: "/p/cicada.mod", Compiler: arrangeCompiler{edition: 2}}
	got, err = convertScore(t, song, manifest)
	if err != nil || strings.Contains(string(got.Source), "cicada 2") {
		t.Fatalf("manifest 2: %v", err)
	}
	// A manifest at edition 1 is refused rather than rewritten.
	var refusal *RefusalError
	if _, err = convertScore(t, song, Options{Edition: 1, ManifestPath: "/p/cicada.mod"}); !errors.As(err, &refusal) || !strings.Contains(refusal.Cause, "arrange needs edition 2") {
		t.Fatalf("manifest 1: %v", err)
	}
}

func TestConvertSongToArrangeKeepOffStop(t *testing.T) {
	scenes := "scene a { bass = pulse drums = beat }\nscene b { bass = keep drums = off }\nscene c { drums = beat }\nscene d { bass = stop }\nscene e { bass = pulse }\n"
	song := templateHead + scenes + "song { a*2 b*2 c*1 d*1 e*1 }\n"
	// Bass: a binds it, b keeps it and c omits it (5 bars), d stops it, e rebinds it.
	// Drums: a binds, b turns off (nothing is emitted), c rebinds and d and e omit it.
	arrange := "arrange {\n" + place("a-1-bass", "bass", "pulse", "@1.1.1", "5bars") + place("a-1-drums", "drums", "beat", "@1.1.1", "2bars") +
		place("c-3-drums", "drums", "beat", "@5.1.1", "3bars") + place("e-5-bass", "bass", "pulse", "@7.1.1", "1bar") + "}\n"
	got, err := convertScore(t, song, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "cicada 2\n" + templateHead + scenes + arrange; string(got.Source) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got.Source, want)
	}
}

func TestConvertSongToArrangeRefusals(t *testing.T) {
	var refusal *RefusalError
	withSettings := templateHead + "scene main { bass = pulse\n  bass.level = -3dB }\nsong { main*2 }\n"
	if _, err := convertScore(t, withSettings, Options{}); !errors.As(err, &refusal) || refusal.Cause != "scene main has settings that arrange cannot carry yet" {
		t.Fatalf("settings: %v", err)
	}
	// Settings refuse before the render check is consulted, even for a scene the song never plays.
	unused := templateHead + "scene main { bass = pulse }\nscene alt { bass = pulse\n  bass.level = -3dB }\nsong { main*2 }\n"
	if _, err := convertScore(t, unused, Options{}); !errors.As(err, &refusal) || refusal.Cause != "scene alt has settings that arrange cannot carry yet" {
		t.Fatalf("unused scene settings: %v", err)
	}
	song := templateHead + templateScenes + "song { main*2 }\n"
	if _, err := applyArrange([]byte(song), `{"kind":"convertsongtoarrange"}`, Options{}); !errors.Is(err, ErrNoRenderCheck) {
		t.Fatalf("nil check: %v", err)
	}
	if _, err := convertScore(t, templateHead+"scene main { }\nsong { main*2 }\n", Options{}); !errors.As(err, &refusal) || !strings.Contains(refusal.Cause, "arrange would be empty") {
		t.Fatalf("empty: %v", err)
	}
	if _, err := convertScore(t, string(namedScore(t)), Options{}); !errors.As(err, &refusal) || refusal.Cause != "the score has no song to convert" {
		t.Fatalf("no song: %v", err)
	}
	differ := &fakeCheck{hashes: map[string]string{"arrange {": "converted"}}
	if _, err := convertScore(t, templateHead+"scene main { bass = pulse }\nsong { main*2 }\n", Options{RenderCheck: differ}); !errors.As(err, &refusal) || refusal.Cause != "the converted score renders differently" {
		t.Fatalf("differs: %v", err)
	}
	// A single entry has no later placement, so no cause is named.
	if _, err := convertScore(t, song, Options{RenderCheck: differ}); !errors.As(err, &refusal) || refusal.Cause != "the converted score renders differently" {
		t.Fatalf("single entry: %v", err)
	}
}

func TestConvertSongToArrangeNamesPhaseCause(t *testing.T) {
	two := "pattern two acid {\n  1 . . . 5 . . . 1 . . . 7 . . . 3 . . . 5 . . . 3 . . . 7 . . .\n}\n"
	source := "title \"t\"\ntrack bass acid {}\n" + two + "scene a { bass = two }\nscene b { bass = two }\nsong { a*3 b*3 }\n"
	differ := &fakeCheck{hashes: map[string]string{"arrange {": "converted"}}
	var refusal *RefusalError
	if _, err := convertScore(t, source, Options{RenderCheck: differ}); !errors.As(err, &refusal) || refusal.Cause != "pattern two (2 bars) would restart out of phase at bar 4" {
		t.Fatalf("phase: %v", err)
	}
	// In phase, the same placements name a drums cause only for drum patterns.
	drums := "title \"t\"\ntrack drums drums {}\npattern beat drums {\n  bd: X... .... X... ....\n}\nscene a { drums = beat }\nscene b { drums = beat }\nsong { a*2 b*2 }\n"
	if _, err := convertScore(t, drums, Options{RenderCheck: differ}); !errors.As(err, &refusal) || refusal.Cause != "drums track drums starts a new placement at bar 3, which renders differently from the song" {
		t.Fatalf("drums: %v", err)
	}
}
