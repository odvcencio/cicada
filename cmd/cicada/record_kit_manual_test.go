package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/cicada/host/recording"
	"m31labs.dev/cicada/internal/testwav"
)

// manualSection returns the lines of the drum kit section of the manual.
func manualSection(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repositoryRoot(), "docs", "manual", "recording-instruments.md"))
	if err != nil {
		t.Fatal(err)
	}
	var section []string
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			in = line == "## Record your own drum kit"
			continue
		}
		if in {
			section = append(section, line)
		}
	}
	if len(section) == 0 {
		t.Fatal("the manual has no Record your own drum kit section")
	}
	return section
}

// manualBlock returns the first fenced block of the given language in the
// section, without the indentation of the list item that holds it. The fence
// shows how far the item is indented, which is more for step 10 than step 9.
func manualBlock(t *testing.T, language string) string {
	t.Helper()
	lines := manualSection(t)
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```"+language {
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
		var block []string
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			block = append(block, strings.TrimPrefix(lines[i], indent))
		}
		return strings.Join(block, "\n") + "\n"
	}
	t.Fatalf("the section has no %s block", language)
	return ""
}

// The manual's recipe, note table and starter score are what the command
// reads, names and writes.
func TestRecordKitManualMatchesTheCommand(t *testing.T) {
	for _, line := range manualSection(t) {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 3 {
			continue
		}
		note, err := strconv.Atoi(strings.TrimSpace(cells[1]))
		if err != nil {
			continue
		}
		if got := strings.Trim(strings.TrimSpace(cells[2]), "`"); got != recording.NoteName(note) {
			t.Errorf("the manual spells note %d as %s, the score spells it %s", note, got, recording.NoteName(note))
		}
	}
	recipe, err := recording.ParseKitRecipe([]byte(manualBlock(t, "json")))
	if err != nil {
		t.Fatal("the manual's recipe is refused:", err)
	}
	drums := map[string]testwav.Drum{"kick": testwav.Kick, "snare": testwav.Snare, "closed_hat": testwav.ClosedHat, "pedal_hat": testwav.ClosedHat, "open_hat": testwav.OpenHat, "crash": testwav.Crash}
	takes := fstest.MapFS{}
	var names []string
	for _, piece := range recipe.Pieces {
		names = append(names, piece.Name)
		for i, take := range piece.Takes {
			level := []float64{.1, .3, .8}[i%3]
			takes[take] = &fstest.MapFile{Data: testwav.Take(48000, drums[piece.Name], level, level*1.05)}
		}
	}
	if strings.Join(names, " ") != "kick snare closed_hat pedal_hat open_hat crash" {
		t.Fatalf("the manual's recipe lists %v", names)
	}
	pack, _, err := recording.BuildKit("studio", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	// The pin and the path differ for every kit; the score around them is
	// the starter the manual shows.
	score := func(source string) string {
		var kept []string
		for _, line := range strings.Split(source, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "pack =") && !strings.HasPrefix(strings.TrimSpace(line), "sha256 =") {
				kept = append(kept, line)
			}
		}
		return strings.Join(kept, "\n")
	}
	if got, want := score(string(pack.Files["instrument.cicada"])), score(manualBlock(t, "cicada")); got != want {
		t.Fatalf("the starter score differs from the manual's:\n%s\nmanual:\n%s", got, want)
	}
}

// The guide gives the frame budget of a piece in frames and in seconds. The
// seconds must be what the limit comes to, or a player plans a take that the
// command then refuses.
func TestRecordKitManualStatesTheFrameBudget(t *testing.T) {
	text := strings.Join(manualSection(t), "\n")
	flat := strings.Join(strings.Fields(text), " ")
	for _, want := range []string{
		"8,388,608 frames, which is " + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/48000))) + " seconds at 48 kHz or " + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/44100))) + " seconds at 44.1 kHz",
		"8,388,608 source frames (" + strconv.Itoa(int(math.Round(float64(recording.MaxFrames)/48000))) + " seconds at 48 kHz",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	if recording.MaxFrames != 8388608 {
		t.Errorf("the limit is %d frames; the guide says 8,388,608", recording.MaxFrames)
	}
	// The worked example: a crash of 12 seconds, 5 hits in each of 3 dynamics.
	limit := float64(recording.MaxFrames) / 48000
	if float64(12*5*3) <= limit || float64(12*4*3) >= limit {
		t.Errorf("the guide's worked example (180 s, or 144 s with 4 hits) does not straddle the limit of %.1f s", limit)
	}
	for _, want := range []string{"takes 180 seconds, which is too long", "Four hits per dynamic take 144 seconds and fit"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
}

// The guide says which attacks the command warns about. A strike less than
// 0.12 s after a hit, such as a flam, is part of that hit and is not counted; an
// attack between 0.12 and 0.5 s after one is kept inside it and is. The numbers
// in the guide must be the ones the analysis uses.
func TestRecordKitManualStatesWhichDoubleStrikesTheCommandWarnsAbout(t *testing.T) {
	flat := strings.Join(strings.Fields(strings.Join(manualSection(t), "\n")), " ")
	for _, want := range []string{
		"An attack that comes between 0.12 and 0.5 seconds after a hit is kept inside that hit, which then plays as a double strike, and the command warns you.",
		"A strike within 0.12 seconds of a hit, such as a flam or a beater bounce, counts as part of that hit, and the command does not warn about it.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	// A louder strike 0.1 s after a soft one is part of it; one 0.14 s after is
	// an attack kept inside it. A half second apart they are two hits.
	for _, test := range []struct {
		apart  float64
		merged int
		hits   int
	}{{.10, 0, 1}, {.14, 1, 1}, {.45, 1, 1}, {.55, 0, 2}} {
		audio := recording.Audio{Rate: 48000}
		one := testwav.Take(48000, testwav.Snare, .2)
		soft, err := recording.DecodeWAV(one)
		if err != nil {
			t.Fatal(err)
		}
		loud, err := recording.DecodeWAV(testwav.Take(48000, testwav.Snare, .8))
		if err != nil {
			t.Fatal(err)
		}
		audio.PCM = make([]float32, 48000*3)
		first, second := 9600, 9600+int(test.apart*48000)
		for i, x := range soft.PCM[9600:] {
			audio.PCM[first+i] += x
		}
		for i, x := range loud.PCM[9600:] {
			if second+i < len(audio.PCM) {
				audio.PCM[second+i] += x
			}
		}
		hits, reports, err := recording.AnalyzeTakes([]recording.Audio{audio}, recording.Options{Root: 38, Layers: 1, Ringing: true})
		if err != nil || len(hits) != test.hits || reports[0].Merged != test.merged {
			t.Errorf("a strike %.2f s after the first: %d hits and %d merged attacks (%v); the guide's numbers say %d and %d", test.apart, len(hits), reports[0].Merged, err, test.hits, test.merged)
		}
	}
}

// The guide says where a hit's slice starts: 3 to 5 ms before its attack, and
// what the command reads as the room when a take ends in silence or opens with
// a noise or with digital silence. The numbers and the rule in the guide must be
// the ones the analysis uses.
func TestRecordKitManualStatesWhereASliceStartsAndWhatTheRoomIs(t *testing.T) {
	flat := strings.Join(strings.Fields(strings.Join(manualSection(t), "\n")), " ")
	for _, want := range []string{
		"Each hit's slice starts 3 to 5 ms before its attack, whatever its tail was doing before the hit",
		"the slice starts before it when the body follows within about 16 ms; a first sound further ahead is left out",
		"A take that opens with a second or more of steady room tone is read from that room tone, even if the file ends in silence that an export added after the last tail, or in a fade-out, which are quieter than any room.",
		"A click or a knock at the very start, such as the recorder starting, does not break the room tone if some 200 ms of the first half second is as quiet as the rest of it.",
		"Digital silence at the start of a file, which an export that begins before your clip adds, does not count as room tone: the room tone is what follows it.",
		"It reads that level as the tail's average over the 90 ms before the last 4 ms of the hit",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	// A take that ends in silence, as an export that runs past the last tail does,
	// still has room tone when it opens with a click, and when digital silence comes
	// first, as the guide says.
	quiet, err := recording.DecodeWAV(testwav.Spaced(48000, testwav.Kick, testwav.Layout{Lead: 3, Spacing: 1.5, After: 2, RoomDB: -70}, .55, .50, .60))
	if err != nil {
		t.Fatal(err)
	}
	silence := make([]float32, 14400)
	click := append([]float32(nil), quiet.PCM...)
	for i := 0; i < 480; i++ {
		click[2400+i] += float32(.01 * math.Sin(float64(i)*.9))
	}
	for _, test := range []struct {
		name string
		pcm  []float32
	}{
		{"a click at 50 ms, then silence added", append(click, silence...)},
		{"digital silence first, then silence added", append(append(make([]float32, 24000), quiet.PCM...), silence...)},
	} {
		_, reports, err := recording.AnalyzeTakes([]recording.Audio{{PCM: test.pcm, Rate: 48000}}, recording.Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || reports[0].NoRoomTone || math.Abs(reports[0].NoiseDB+70) > 3 {
			t.Errorf("%s: %+v (%v); the guide says the room tone is found and read at -70 dBFS", test.name, reports, err)
		}
	}
	// The drums of the guide's kit, a second or more apart in a quiet room after
	// three seconds of silence: a cymbal and the open hi-hat need longer.
	for _, test := range []struct {
		drum    testwav.Drum
		spacing float64
	}{{testwav.Kick, 1}, {testwav.Snare, 1}, {testwav.ClosedHat, 1}, {testwav.OpenHat, 2}, {testwav.Crash, 3}} {
		audio, err := recording.DecodeWAV(testwav.Spaced(48000, test.drum, testwav.Layout{Lead: 3, Spacing: test.spacing, After: 2, RoomDB: -70}, .5, .45, .55, .5))
		if err != nil {
			t.Fatal(err)
		}
		hits, reports, err := recording.AnalyzeTakes([]recording.Audio{audio}, recording.Options{Root: 36, Layers: 1, Ringing: true})
		if err != nil || len(hits) != 4 || reports[0].NoRoomTone {
			t.Fatalf("%s: %d hits, %+v, %v", test.drum, len(hits), reports, err)
		}
		for i, h := range hits {
			played := int(math.Round((3 + float64(float64(i)*test.spacing)) * 48000))
			if lead := float64(played-h.Start) / 48; lead < 3 || lead > 5 {
				t.Errorf("%s: hit %d starts %.1f ms before its attack; the guide says 3 to 5 ms", test.drum, i+1, lead)
			}
		}
	}
}
