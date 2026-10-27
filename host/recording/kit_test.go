package recording

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/internal/testwav"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
	"m31labs.dev/cicada/render"
)

// sessionKit synthesizes a recording session: a folder of takes, one file per
// piece and dynamic as the guide asks, and the recipe that names them. The
// audio is noise bursts and decaying sines made in the test; none is stored.
func sessionKit() (KitRecipe, fstest.MapFS) {
	takes := fstest.MapFS{}
	add := func(name string, drum testwav.Drum, levels ...float64) string {
		takes[name] = &fstest.MapFile{Data: testwav.Take(48000, drum, levels...)}
		return name
	}
	return KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "kick", Note: 36, Layers: 3, OneShot: true, Takes: []string{
			add("kick-soft.wav", testwav.Kick, .08, .09, .10, .085, .095),
			add("kick-medium.wav", testwav.Kick, .28, .30, .32, .29, .31),
			add("kick-hard.wav", testwav.Kick, .78, .80, .82, .79, .81),
		}},
		{Name: "snare", Note: 38, Layers: 2, OneShot: true, Takes: []string{
			add("snare-soft.wav", testwav.Snare, .05, .06, .055, .065),
			add("snare-hard.wav", testwav.Snare, .50, .55, .60, .52),
		}},
		{Name: "closed_hat", Note: 42, Layers: 2, OneShot: true, Choke: "hat", Takes: []string{
			add("closed-soft.wav", testwav.ClosedHat, .07, .08, .075),
			add("closed-hard.wav", testwav.ClosedHat, .35, .38, .36),
		}},
		{Name: "pedal_hat", Note: 44, Layers: 1, OneShot: true, Choke: "hat", Takes: []string{
			add("pedal.wav", testwav.ClosedHat, .12, .13, .125),
		}},
		{Name: "open_hat", Note: 46, Layers: 1, OneShot: true, Choke: "hat", Takes: []string{
			add("open.wav", testwav.OpenHat, .40, .42, .41, .43),
		}},
		{Name: "crash", Note: 49, Layers: 1, OneShot: true, ChokeNote: 26, Takes: []string{
			add("crash.wav", testwav.Crash, .50, .52, .51),
		}},
		{Name: "ride", Note: 51, Layers: 1, OneShot: false, GainDB: -6, Takes: []string{
			add("ride.wav", testwav.Crash, .30, .31, .32),
		}},
	}}, takes
}

var sessionHits = map[string]int{"kick": 15, "snare": 8, "closed_hat": 6, "pedal_hat": 3, "open_hat": 4, "crash": 3, "ride": 3}

func buildSession(t *testing.T) (*Pack, KitReport, KitRecipe, fstest.MapFS) {
	t.Helper()
	recipe, takes := sessionKit()
	pack, report, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	return pack, report, recipe, takes
}

func TestParseKitRecipe(t *testing.T) {
	recipe, err := ParseKitRecipe([]byte(`{
  "format": "cicada.kit-recipe/1",
  "pieces": [
    {"name": "kick", "note": 36, "takes": ["kick-soft.wav", "takes/kick-hard.wav"]},
    {"name": "closed_hat", "note": 42, "takes": ["closed.wav"], "layers": 2, "choke": "hat", "gain_db": -3.5},
    {"name": "crash", "note": 49, "takes": ["crash.wav"], "layers": 1, "choke_note": 26, "one_shot": true},
    {"name": "ride", "note": 51, "takes": ["ride.wav"], "one_shot": false}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	want := KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "kick", Note: 36, Takes: []string{"kick-soft.wav", "takes/kick-hard.wav"}, Layers: 3, OneShot: true},
		{Name: "closed_hat", Note: 42, Takes: []string{"closed.wav"}, Layers: 2, Choke: "hat", OneShot: true, GainDB: -3.5},
		{Name: "crash", Note: 49, Takes: []string{"crash.wav"}, Layers: 1, ChokeNote: 26, OneShot: true},
		{Name: "ride", Note: 51, Takes: []string{"ride.wav"}, Layers: 3, OneShot: false},
	}}
	if !reflect.DeepEqual(recipe, want) {
		t.Fatalf("parsed %+v\nwant   %+v", recipe, want)
	}
}

// Windows editors may save JSON with a byte order mark, and with CRLF lines.
func TestParseKitRecipeAcceptsByteOrderMarkAndCRLF(t *testing.T) {
	text := "{\r\n  \"format\": \"cicada.kit-recipe/1\",\r\n  \"pieces\": [\r\n    {\"name\": \"kick\", \"note\": 36, \"takes\": [\"kick.wav\"]}\r\n  ]\r\n}\r\n"
	recipe, err := ParseKitRecipe(append([]byte("\xef\xbb\xbf"), text...))
	if err != nil || len(recipe.Pieces) != 1 || recipe.Pieces[0].Name != "kick" {
		t.Fatalf("recipe %+v, error %v", recipe, err)
	}
	// An error still points at the right line.
	_, err = ParseKitRecipe(append([]byte("\xef\xbb\xbf"), strings.Replace(text, `"note": 36`, `"note": }`, 1)...))
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("error %v; want it to name line 4", err)
	}
}

func TestKitRecipeValidation(t *testing.T) {
	piece := func(fields string) string {
		return `{"name":"kick","note":36,"takes":["kick.wav"]` + fields + `}`
	}
	wrap := func(pieces ...string) string {
		return `{"format":"cicada.kit-recipe/1","pieces":[` + strings.Join(pieces, ",") + `]}`
	}
	other := `{"name":"snare","note":38,"takes":["snare.wav"]}`
	var many []string
	for i := 0; i < MaxKitPieces+1; i++ {
		many = append(many, fmt.Sprintf(`{"name":"p%d","note":%d,"takes":["a.wav"]}`, i, MinKitNote+i%80))
	}
	var takes []string
	for i := 0; i < MaxKitTakes+1; i++ {
		takes = append(takes, fmt.Sprintf(`"t%d.wav"`, i))
	}
	cases := []struct{ name, recipe, want string }{
		{"empty", ``, "empty or ends early"},
		{"syntax", "{\n\"format\": }", "line 2"},
		{"trailing text", wrap(piece(``)) + ` {}`, "unexpected text"},
		{"missing format", `{"pieces":[` + piece(``) + `]}`, `"format" must be`},
		{"other format", strings.Replace(wrap(piece(``)), "kit-recipe/1", "kit-recipe/2", 1), `"format" must be`},
		{"no pieces", wrap(), "1 to 64 pieces"},
		{"too many pieces", wrap(many...), "1 to 64 pieces"},
		{"misspelled option", wrap(piece(`,"layerz":3`)), `unknown field "layerz"`},
		{"upper-case name", wrap(`{"name":"Kick","note":36,"takes":["a.wav"]}`), `"name" must be lowercase`},
		{"missing name", wrap(`{"note":36,"takes":["a.wav"]}`), `"name" must be lowercase`},
		{"duplicate name", wrap(piece(``), piece(``)), "another piece already has this name"},
		{"missing note", wrap(`{"name":"kick","takes":["a.wav"]}`), `piece 1 (kick): "note" is missing`},
		{"text note", wrap(`{"name":"kick","note":"36","takes":["a.wav"]}`), `should be a whole number, not string`},
		{"recipe is a list", `[]`, "the recipe should be an object, not array"},
		{"pieces is an object", `{"format":"cicada.kit-recipe/1","pieces":{}}`, `"pieces" should be a list, not object`},
		{"piece is a number", `{"format":"cicada.kit-recipe/1","pieces":[1]}`, `every entry of "pieces" should be an object, not number`},
		{"takes is text", wrap(`{"name":"kick","note":36,"takes":"kick.wav"}`), `"pieces.takes" should be a list, not string`},
		{"one_shot is text", wrap(piece(`,"one_shot":"yes"`)), `"pieces.one_shot" should be true or false, not string`},
		{"fractional note", wrap(`{"name":"kick","note":36.5,"takes":["a.wav"]}`), `should be a whole number, not number 36.5`},
		{"low note", wrap(`{"name":"kick","note":11,"takes":["a.wav"]}`), `"note" 11 is outside 12 to 95`},
		{"high note", wrap(`{"name":"kick","note":96,"takes":["a.wav"]}`), `"note" 96 is outside 12 to 95`},
		{"overlapping notes", wrap(piece(``), `{"name":"rim","note":36,"takes":["rim.wav"]}`), `piece "kick" and piece "rim" both use note 36 (c2)`},
		{"choke note on a piece note", wrap(other, piece(`,"choke_note":38`)), `piece "snare" and the choke note of piece "kick" both use note 38 (d2)`},
		{"shared choke note", wrap(piece(`,"choke_note":26`), `{"name":"crash","note":49,"takes":["c.wav"],"choke_note":26}`), "both use note 26 (d1)"},
		{"choke note out of range", wrap(piece(`,"choke_note":0`)), `"choke_note" 0 is outside`},
		{"no takes", wrap(`{"name":"kick","note":36,"takes":[]}`), `"takes" must list 1 to 32`},
		{"missing takes", wrap(`{"name":"kick","note":36}`), `"takes" must list 1 to 32`},
		{"too many takes", wrap(`{"name":"kick","note":36,"takes":[` + strings.Join(takes, ",") + `]}`), `"takes" must list 1 to 32`},
		{"empty take name", wrap(`{"name":"kick","note":36,"takes":[""]}`), "must be a path inside the recipe's folder"},
		{"absolute take", wrap(`{"name":"kick","note":36,"takes":["/etc/kick.wav"]}`), "must be a path inside the recipe's folder"},
		{"parent take", wrap(`{"name":"kick","note":36,"takes":["../kick.wav"]}`), "must be a path inside the recipe's folder"},
		{"backslash take", wrap(`{"name":"kick","note":36,"takes":["a\\kick.wav"]}`), "must be a path inside the recipe's folder"},
		{"repeated take", wrap(`{"name":"kick","note":36,"takes":["a.wav","a.wav"]}`), `take "a.wav" is listed twice`},
		{"no layers", wrap(piece(`,"layers":0`)), `"layers" 0 is outside 1 to 8`},
		{"too many layers", wrap(piece(`,"layers":9`)), `"layers" 9 is outside 1 to 8`},
		{"bad group name", wrap(piece(`,"choke":"Hat"`)), `"choke" must be a lowercase group name`},
		{"group and choke note", wrap(piece(`,"choke":"hat","choke_note":26`)), `use either "choke" or "choke_note"`},
		{"loud trim", wrap(piece(`,"gain_db":12.5`)), `"gain_db" 12.5 is outside -24 to 12`},
		{"quiet trim", wrap(piece(`,"gain_db":-25`)), `"gain_db" -25 is outside -24 to 12`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseKitRecipe([]byte(test.recipe))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v; want it to mention %q", err, test.want)
			}
		})
	}
	if _, err := ParseKitRecipe(bytes.Repeat([]byte(" "), MaxKitRecipeBytes+1)); err == nil {
		t.Fatal("oversized recipe accepted")
	}
}

func TestKitZoneMapPerNote(t *testing.T) {
	pack, report, recipe, _ := buildSession(t)
	if report.Hits != 42 || report.Zones != 43 || len(pack.Manifest.Assets) != 42 || len(pack.Hits) != 42 {
		t.Fatalf("report %+v: want 42 hits and 43 zones (one silent choke note)", report)
	}
	maxPeak := 0.0
	for _, h := range pack.Hits {
		maxPeak = max(maxPeak, h.Peak)
	}
	assetAt := map[string]int{}
	for i, a := range pack.Manifest.Assets {
		assetAt[a.ID] = i
	}
	pieceGroup := map[int]string{}
	chokeOf := map[string]int{}
	for i, piece := range recipe.Pieces {
		var zones []Zone
		for _, z := range pack.Manifest.Zones {
			if z.Root == piece.Note {
				zones = append(zones, z)
			}
		}
		if report.Pieces[i].Name != piece.Name || report.Pieces[i].Hits != sessionHits[piece.Name] || len(zones) != sessionHits[piece.Name] {
			t.Fatalf("%s: report %+v and %d zones; want %d hits", piece.Name, report.Pieces[i], len(zones), sessionHits[piece.Name])
		}
		layers := map[int][]Zone{}
		trim := math.Pow(10, piece.GainDB/20)
		for _, z := range zones {
			if z.KeyLow != piece.Note || z.KeyHigh != piece.Note {
				t.Errorf("%s: zone covers keys %d-%d, want only %d", piece.Name, z.KeyLow, z.KeyHigh, piece.Note)
			}
			if z.OneShot != piece.OneShot || z.ChokeSustain != (piece.ChokeNote != 0) {
				t.Errorf("%s: one-shot %v, choke sustain %v", piece.Name, z.OneShot, z.ChokeSustain)
			}
			if !strings.HasPrefix(z.Asset, piece.Name+"_") {
				t.Errorf("%s: zone plays asset %s", piece.Name, z.Asset)
			}
			if other, taken := pieceGroup[z.Group]; taken && other != piece.Name {
				t.Errorf("%s and %s share key group %d", piece.Name, other, z.Group)
			}
			pieceGroup[z.Group] = piece.Name
			// The loudest hit in the kit plays at full scale; the others keep
			// their recorded level against it, then the piece's trim applies.
			want := pack.Hits[assetAt[z.Asset]].Peak / maxPeak * trim
			if math.Abs(z.Gain-want) > 1e-5 {
				t.Errorf("%s: gain %g, want %g", piece.Name, z.Gain, want)
			}
			layers[z.Layer] = append(layers[z.Layer], z)
		}
		if len(layers) != piece.Layers {
			t.Fatalf("%s: %d layers, want %d", piece.Name, len(layers), piece.Layers)
		}
		centres := make([]int, 0, len(layers))
		for centre := range layers {
			centres = append(centres, centre)
		}
		sort.Ints(centres)
		previousHigh, previousLoudest := 0, math.Inf(-1)
		for n, centre := range centres {
			members := layers[centre]
			low, high := 1+n*127/piece.Layers, (n+1)*127/piece.Layers
			positions := map[int]bool{}
			quietest, loudest := math.Inf(1), math.Inf(-1)
			for _, z := range members {
				if z.VelocityLow != low || z.VelocityHigh != high || centre < low || centre > high {
					t.Errorf("%s: layer %d spans velocity %d-%d around %d, want %d-%d", piece.Name, n+1, z.VelocityLow, z.VelocityHigh, centre, low, high)
				}
				if z.Count != len(members) || z.Position < 0 || z.Position >= z.Count || positions[z.Position] {
					t.Errorf("%s: layer %d round robin %d of %d with %d takes", piece.Name, n+1, z.Position, z.Count, len(members))
				}
				positions[z.Position] = true
				level := pack.Hits[assetAt[z.Asset]].LoudnessDB
				quietest, loudest = min(quietest, level), max(loudest, level)
			}
			if low != previousHigh+1 || quietest <= previousLoudest {
				t.Errorf("%s: layer %d does not follow layer %d in velocity (%d) and loudness (%.1f dB over %.1f dB)", piece.Name, n+1, n, low, quietest, previousLoudest)
			}
			previousHigh, previousLoudest = high, loudest
			if got := report.Pieces[i].Layers[n]; got.Hits != len(members) || got.MinDB != quietest || got.MaxDB != loudest {
				t.Errorf("%s: layer %d reported as %+v", piece.Name, n+1, got)
			}
		}
		if previousHigh != 127 {
			t.Errorf("%s: velocities stop at %d", piece.Name, previousHigh)
		}
		chokeOf[piece.Name] = zones[0].ChokeGroup
	}
	// The three hats share one choke group. The crash has a group of its own,
	// and drums that ring through, such as the kick, have none.
	hat := chokeOf["closed_hat"]
	if hat == 0 || chokeOf["pedal_hat"] != hat || chokeOf["open_hat"] != hat {
		t.Errorf("hat choke groups %d %d %d", chokeOf["closed_hat"], chokeOf["pedal_hat"], chokeOf["open_hat"])
	}
	if chokeOf["crash"] == 0 || chokeOf["crash"] == hat || chokeOf["kick"] != 0 || chokeOf["snare"] != 0 || chokeOf["ride"] != 0 {
		t.Errorf("choke groups %v", chokeOf)
	}
	// The choke note is a silent zone that damps the crash's group.
	var control []Zone
	for _, z := range pack.Manifest.Zones {
		if z.Root == 26 {
			control = append(control, z)
		}
	}
	if len(control) != 1 {
		t.Fatalf("%d zones on the choke note, want 1", len(control))
	}
	c := control[0]
	if c.KeyLow != 26 || c.KeyHigh != 26 || c.Gain != 0 || c.ChokeGroup != chokeOf["crash"] || c.ChokeSustain || c.VelocityLow != 1 || c.VelocityHigh != 127 || c.Count != 1 || c.Position != 0 {
		t.Errorf("choke note zone %+v", c)
	}
	if _, used := pieceGroup[c.Group]; used {
		t.Errorf("choke note shares key group %d with piece %s", c.Group, pieceGroup[c.Group])
	}
}

// The pack must pass the runtime's own admission, and sound only on its notes.
func TestKitPackLoadsAndPlaysExactlyItsNotes(t *testing.T) {
	pack, _, recipe, _ := buildSession(t)
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", pack.Pin)
	if err != nil {
		t.Fatal(err)
	}
	mapped := map[int]bool{26: true}
	for _, piece := range recipe.Pieces {
		mapped[piece.Note] = true
	}
	for note := 0; note < 128; note++ {
		voice, err := prepared.New(48000)
		if err != nil {
			t.Fatal(err)
		}
		_, err = voice.NoteOn(uint8(note), 100)
		if mapped[note] != (err == nil) {
			t.Errorf("note %d (%s): mapped %v, NoteOn error %v", note, NoteName(note), mapped[note], err)
		}
	}
}

// Takes made at other sample rates must load for every output rate.
func TestKitLoadsAtEveryOutputRate(t *testing.T) {
	takes := fstest.MapFS{
		"kick.wav":  {Data: testwav.Take(44100, testwav.Kick, .8, .82, .81)},
		"crash.wav": {Data: testwav.Take(96000, testwav.Crash, .5, .52, .51)},
	}
	recipe := KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "kick", Note: 36, Takes: []string{"kick.wav"}, Layers: 1, OneShot: true},
		{Name: "crash", Note: 49, Takes: []string{"crash.wav"}, Layers: 1, OneShot: true},
	}}
	pack, _, err := BuildKit("rates", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	for _, rate := range []int{44100, 48000, 96000} {
		prepared, err := instrumentpack.LoadAtRate(dir, "manifest.json", pack.Pin, rate)
		if err != nil {
			t.Fatalf("output rate %d: %v", rate, err)
		}
		voice, err := prepared.New(rate)
		if err != nil {
			t.Fatalf("output rate %d: %v", rate, err)
		}
		if _, err := voice.NoteOn(49, 100); err != nil {
			t.Fatalf("output rate %d: %v", rate, err)
		}
	}
}

func TestKitVelocityLayersAndRoundRobinsPlayInOrder(t *testing.T) {
	pack, _, _, _ := buildSession(t)
	level := func(velocity, cycle int) (int, float64) {
		t.Helper()
		_, index, err := pack.Audition(36, velocity, cycle)
		if err != nil {
			t.Fatal(err)
		}
		return index, pack.Hits[index].LoudnessDB
	}
	var loudest [3]float64
	for n, velocity := range []int{10, 60, 120} {
		seen := map[int]bool{}
		loudest[n] = math.Inf(-1)
		for cycle := 0; cycle < 5; cycle++ {
			index, db := level(velocity, cycle)
			if !strings.HasPrefix(pack.Manifest.Assets[index].ID, "kick_") || seen[index] {
				t.Fatalf("velocity %d cycle %d plays %s again or from another piece", velocity, cycle, pack.Manifest.Assets[index].ID)
			}
			seen[index] = true
			loudest[n] = max(loudest[n], db)
		}
		if again, _ := level(velocity, 5); !seen[again] || again != firstIndex(t, pack, velocity) {
			t.Fatalf("velocity %d: the round robin does not wrap after five takes", velocity)
		}
	}
	if !(loudest[0] < loudest[1] && loudest[1] < loudest[2]) {
		t.Fatalf("louder velocities should play louder layers, got %v dB", loudest)
	}
}

func firstIndex(t *testing.T, pack *Pack, velocity int) int {
	t.Helper()
	_, index, err := pack.Audition(36, velocity, 0)
	if err != nil {
		t.Fatal(err)
	}
	return index
}

func TestKitGainTrimAndOneShotAreRecorded(t *testing.T) {
	recipe, takes := sessionKit()
	loud, _, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	for i := range recipe.Pieces {
		if recipe.Pieces[i].Name == "ride" {
			recipe.Pieces[i].GainDB = 0
		}
	}
	flat, _, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for i, z := range flat.Manifest.Zones {
		if z.Root != 51 {
			if loud.Manifest.Zones[i].Gain != z.Gain {
				t.Fatalf("a trim on the ride changed the gain of note %d", z.Root)
			}
			continue
		}
		if ratio := loud.Manifest.Zones[i].Gain / z.Gain; math.Abs(ratio-math.Pow(10, -6.0/20)) > 1e-5 {
			t.Fatalf("a -6 dB trim scaled the ride by %g", ratio)
		}
		if z.OneShot {
			t.Fatal("one_shot false was not kept")
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("checked %d ride zones, want 3", checked)
	}
}

func TestKitBuildRefusals(t *testing.T) {
	good := func() (KitRecipe, fstest.MapFS) { return sessionKit() }
	nan := make([]float32, 9600)
	nan[100] = float32(math.NaN())
	cases := []struct {
		name   string
		mutate func(*KitRecipe, fstest.MapFS) string
		want   string
	}{
		{"missing take", func(r *KitRecipe, f fstest.MapFS) string { delete(f, "kick-soft.wav"); return "session" }, `piece "kick": take "kick-soft.wav": not found; takes are looked up next to the recipe`},
		{"too few hits for the layers", func(r *KitRecipe, f fstest.MapFS) string {
			r.Pieces[0].Takes = []string{"kick-hard.wav"}
			f["kick-hard.wav"] = &fstest.MapFile{Data: testwav.Take(48000, testwav.Kick, .8, .81)}
			return "session"
		}, `piece "kick": found 2 hits, but 3 velocity layers need at least 3`},
		{"only silence", func(r *KitRecipe, f fstest.MapFS) string {
			r.Pieces[0].Takes = []string{"silence.wav"}
			f["silence.wav"] = &fstest.MapFile{Data: testwav.Bytes(48000, 1, 24, 48000, 1)}
			return "session"
		}, `piece "kick": no hits detected`},
		{"one take without hits", func(r *KitRecipe, f fstest.MapFS) string {
			f["kick-soft.wav"] = &fstest.MapFile{Data: testwav.Bytes(48000, 1, 24, 48000, 1)}
			return "session"
		}, `piece "kick": take "kick-soft.wav" has no hits`},
		{"not a WAV file", func(r *KitRecipe, f fstest.MapFS) string {
			f["kick-soft.wav"] = &fstest.MapFile{Data: []byte("not audio")}
			return "session"
		}, `piece "kick": take "kick-soft.wav": expected valid PCM`},
		{"non-finite audio", func(r *KitRecipe, f fstest.MapFS) string {
			f["kick-soft.wav"] = &fstest.MapFile{Data: EncodeWAV(nan, 48000)}
			return "session"
		}, `piece "kick": take "kick-soft.wav": invalid PCM`},
		{"over the frame limit", func(r *KitRecipe, f fstest.MapFS) string {
			f["kick-soft.wav"] = &fstest.MapFile{Data: testwav.Bytes(48000, 1, 24, MaxFrames+1, 1)}
			return "session"
		}, `piece "kick": take "kick-soft.wav": recording exceeds frame or sample-rate limit`},
		{"more than 32 hits in a layer", func(r *KitRecipe, f fstest.MapFS) string {
			levels := make([]float64, 33)
			for i := range levels {
				levels[i] = .5
			}
			r.Pieces[0].Takes, r.Pieces[0].Layers = []string{"many.wav"}, 1
			f["many.wav"] = &fstest.MapFile{Data: testwav.Take(48000, testwav.Kick, levels...)}
			return "session"
		}, `piece "kick": more than 32 hits in velocity layer 1`},
		{"too low a sample rate", func(r *KitRecipe, f fstest.MapFS) string {
			// 8 kHz audio would need a 12x ratio to play at 96 kHz.
			f["kick-soft.wav"] = &fstest.MapFile{Data: testwav.Take(8000, testwav.Kick, .08, .09, .10, .085, .095)}
			return "session"
		}, `piece "kick": take "kick-soft.wav" is sampled at 8000 Hz, too low to play at every output rate`},
		{"reserved kit name", func(r *KitRecipe, f fstest.MapFS) string { return "drums" }, "acid, drums, guitar and audio are reserved"},
		{"invalid kit name", func(r *KitRecipe, f fstest.MapFS) string { return "My Kit" }, "use a lowercase kit name"},
		{"invalid recipe", func(r *KitRecipe, f fstest.MapFS) string { r.Pieces[1].Note = 36; return "session" }, "both use note 36 (c2)"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			recipe, takes := good()
			name := test.mutate(&recipe, takes)
			pack, _, err := BuildKit(name, recipe, takes)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v; want it to mention %q", err, test.want)
			}
			if pack != nil {
				t.Fatal("a refused build returned a pack")
			}
		})
	}
}

// A tom recorded once can play on two General MIDI notes: two pieces may name
// the same take file.
func TestKitPiecesMayShareATakeFile(t *testing.T) {
	takes := fstest.MapFS{"tom.wav": {Data: testwav.Take(48000, testwav.Kick, .4, .42, .41)}}
	recipe := KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "low_tom", Note: 45, Takes: []string{"tom.wav"}, Layers: 1, OneShot: true},
		{Name: "mid_tom", Note: 47, Takes: []string{"tom.wav"}, Layers: 1, OneShot: true},
	}}
	pack, report, err := BuildKit("toms", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	if report.Hits != 6 || len(pack.Manifest.Zones) != 6 {
		t.Fatalf("report %+v", report)
	}
	for i := 0; i < 3; i++ {
		low, mid := pack.Manifest.Assets[i], pack.Manifest.Assets[i+3]
		if low.SHA256 != mid.SHA256 || low.ID == mid.ID || low.SourceSHA256 != mid.SourceSHA256 {
			t.Fatalf("take %d differs between the two notes: %+v %+v", i+1, low, mid)
		}
	}
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := instrumentpack.Load(dir, "manifest.json", pack.Pin); err != nil {
		t.Fatal(err)
	}
}

func TestKitBuildsAreByteIdentical(t *testing.T) {
	first, report, recipe, takes := buildSession(t)
	for i := 0; i < 3; i++ {
		again, againReport, err := BuildKit("session", recipe, takes)
		if err != nil {
			t.Fatal(err)
		}
		if again.Pin != first.Pin || !reflect.DeepEqual(again.Files, first.Files) || !reflect.DeepEqual(againReport, report) {
			t.Fatalf("build %d differs from the first", i+2)
		}
	}
	dir := filepath.Join(t.TempDir(), "kit")
	if err := first.Write(dir); err != nil {
		t.Fatal(err)
	}
	// A directory is reused only when every generated file is identical.
	if err := first.Write(dir); err != nil {
		t.Fatal("identical rebuild refused:", err)
	}
	names := make([]string, 0, len(first.Files))
	for name := range first.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range []string{names[0], "manifest.json", "instrument.cicada", names[len(names)-1]} {
		path := filepath.Join(dir, name)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(original[:len(original):len(original)], '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		if first.Write(dir) == nil {
			t.Fatalf("reused a directory whose %s differs", name)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if first.Write(dir) == nil {
			t.Fatalf("reused a directory missing %s", name)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Changing one gain trim changes the pin, so it needs a new directory.
	recipe.Pieces[0].GainDB = -1
	changed, _, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Pin == first.Pin || changed.Write(dir) == nil {
		t.Fatal("a different kit was written over an existing pack")
	}
}

func TestKitLicenseLabelAndProvenance(t *testing.T) {
	pack, _, recipe, _ := buildSession(t)
	for _, a := range pack.Manifest.Assets {
		if a.License != "owner recording" || a.SourceURL != "" || a.LicenseURL != "" || a.Attribution != "" || len(a.SourceSHA256) != 64 || a.Channels != 1 {
			t.Fatalf("asset %+v", a)
		}
	}
	if !strings.HasPrefix(string(pack.Files["LICENSE.txt"]), "owner recording\n") {
		t.Fatalf("LICENSE.txt: %q", pack.Files["LICENSE.txt"])
	}
	// The kit uses the label, never the single-instrument one, and the loader
	// accepts it.
	for name, data := range pack.Files {
		if strings.HasSuffix(name, ".gz") {
			continue
		}
		if strings.Contains(string(data), "user recording") || strings.Contains(string(data), "User recording") {
			t.Errorf("%s uses the user recording label", name)
		}
		for _, piece := range recipe.Pieces {
			for _, take := range piece.Takes {
				if strings.Contains(string(data), take) {
					t.Errorf("%s names the take file %s", name, take)
				}
			}
		}
	}
	if _, err := instrumentpack.DecodeManifest(pack.Files["manifest.json"]); err != nil {
		t.Fatal(err)
	}
	var analysis []map[string]any
	if err := json.Unmarshal(pack.Files["analysis.json"], &analysis); err != nil || len(analysis) != len(pack.Manifest.Assets) {
		t.Fatalf("analysis.json: %v, %d records", err, len(analysis))
	}
	for i, record := range analysis {
		if record["asset"] != pack.Manifest.Assets[i].ID || !strings.HasPrefix(fmt.Sprint(record["asset"]), fmt.Sprint(record["piece"])+"_") {
			t.Fatalf("analysis record %d: %v", i, record)
		}
	}
}

// renderKit renders a one-bar score that plays steps of the pack's sampler
// track at 120 BPM, where a step lasts 125 ms.
func renderKit(t *testing.T, pack *Pack, steps ...int) []float32 {
	t.Helper()
	cells := make([]string, 16)
	for i := range cells {
		cells[i] = "."
		if i < len(steps) && steps[i] != 0 {
			cells[i] = NoteName(steps[i])
		}
	}
	source := fmt.Sprintf("cicada 2\ntempo 120\n%s\ntrack kit %s {}\npattern p notes { %s }\nscene main { kit = p }\nsong { main }\n", pack.Source("manifest.json", 36), pack.Manifest.ID, strings.Join(cells, " "))
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatal(d.Error())
		}
	}
	var out bytes.Buffer
	if _, err := render.WAV(score, render.Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .5}, &out); err != nil {
		t.Fatal(err)
	}
	audio, err := DecodeWAV(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return audio.PCM
}

func rms(pcm []float32, from, to float64) float64 {
	a, b := int(from*48000), min(len(pcm), int(to*48000))
	var sum float64
	for _, x := range pcm[a:b] {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum / float64(b-a))
}

func hatKit(t *testing.T, choke string) *Pack {
	t.Helper()
	takes := fstest.MapFS{
		"kick.wav":   {Data: testwav.Take(48000, testwav.Kick, .8, .82, .81)},
		"closed.wav": {Data: testwav.Take(48000, testwav.ClosedHat, .4, .42, .41)},
		"pedal.wav":  {Data: testwav.Take(48000, testwav.ClosedHat, .2, .21, .22)},
		"open.wav":   {Data: testwav.Take(48000, testwav.OpenHat, .4, .42, .41)},
	}
	recipe := KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "kick", Note: 36, Takes: []string{"kick.wav"}, Layers: 1, OneShot: true},
		{Name: "closed_hat", Note: 42, Takes: []string{"closed.wav"}, Layers: 1, OneShot: true, Choke: choke},
		{Name: "pedal_hat", Note: 44, Takes: []string{"pedal.wav"}, Layers: 1, OneShot: true, Choke: choke},
		{Name: "open_hat", Note: 46, Takes: []string{"open.wav"}, Layers: 1, OneShot: true, Choke: choke},
	}}
	pack, _, err := BuildKit("hats", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

// An open hat that a closed or pedal hat follows is cut; nothing else cuts it.
func TestKitClosedAndPedalHatsCutOpenHatInRender(t *testing.T) {
	choked := hatKit(t, "hat")
	// The open hat starts at 0 s and the second note at 0.25 s. The closed
	// and pedal hats are done by 0.4 s, so 0.5 to 0.9 s hears only the open hat.
	alone := rms(renderKit(t, choked, 46), .5, .9)
	if alone < .002 {
		t.Fatalf("the open hat alone is too quiet to measure: rms %g", alone)
	}
	cases := []struct {
		name    string
		kit     *Pack
		note    int
		cutting bool
	}{
		{"closed hat", choked, 42, true},
		{"pedal hat", choked, 44, true},
		{"kick", choked, 36, false},
		{"closed hat without a choke group", hatKit(t, ""), 42, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			after := rms(renderKit(t, test.kit, 46, 0, test.note), .5, .9)
			if test.cutting && (after > alone/1000 || after > 1e-5) {
				t.Fatalf("open hat survived: rms %g after a %s, %g on its own", after, test.name, alone)
			}
			if !test.cutting && after < alone/2 {
				t.Fatalf("open hat was cut by a %s: rms %g, %g on its own", test.name, after, alone)
			}
		})
	}
}

func TestKitChokeNoteDampsCymbalInRender(t *testing.T) {
	pack, _, _, _ := buildSession(t)
	// The crash starts at 0 s; the choke note, if played, at 0.5 s.
	ringing := rms(renderKit(t, pack, 49), .6, 1)
	if ringing < .002 {
		t.Fatalf("the crash alone is too quiet to measure: rms %g", ringing)
	}
	if damped := rms(renderKit(t, pack, 49, 0, 0, 0, 26), .6, 1); damped > ringing/1000 || damped > 1e-5 {
		t.Fatalf("the choke note left the crash at rms %g, against %g undamped", damped, ringing)
	}
	// The open hat is in another group, so the crash's choke note leaves it.
	openAlone := rms(renderKit(t, pack, 46), .6, 1)
	if openAlone < .001 {
		t.Fatalf("the open hat alone is too quiet to measure: rms %g", openAlone)
	}
	if open := rms(renderKit(t, pack, 46, 0, 0, 0, 26), .6, 1); open < openAlone/2 {
		t.Fatalf("the crash's choke note damped the open hat: rms %g, %g undamped", open, openAlone)
	}
}

// A piece that is not one-shot stops with its note; a one-shot piece plays on.
func TestKitOneShotOptionDecidesWhetherNoteLengthMatters(t *testing.T) {
	recipe, takes := sessionKit()
	gated, _, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	for i := range recipe.Pieces {
		if recipe.Pieces[i].Name == "ride" {
			recipe.Pieces[i].OneShot = true
		}
	}
	ringing, _, err := BuildKit("session", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	// A step lasts 125 ms and a note is held for 55% of it, so the gated ride
	// is gone by 0.3 s, while the one-shot ride rings for seconds.
	if got := rms(renderKit(t, gated, 51), .4, .9); got > 1e-5 {
		t.Fatalf("a ride that is not one-shot still sounds at 0.4 s: rms %g", got)
	}
	if got := rms(renderKit(t, ringing, 51), .4, .9); got < .002 {
		t.Fatalf("a one-shot ride stopped with its note: rms %g", got)
	}
}

// Voices are counted directly: a cymbal's hits overlap, a hat cuts its group.
func TestKitChokeBehaviorOfTheLoadedInstrument(t *testing.T) {
	pack, _, _, _ := buildSession(t)
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", pack.Pin)
	if err != nil {
		t.Fatal(err)
	}
	play := func(notes ...uint8) int {
		voice, err := prepared.New(48000)
		if err != nil {
			t.Fatal(err)
		}
		var left, right [1024]float32
		for _, note := range notes {
			if _, err := voice.NoteOn(note, 100); err != nil {
				t.Fatal(err)
			}
			voice.Render(left[:], right[:])
		}
		return voice.ActiveVoices()
	}
	for _, test := range []struct {
		name  string
		notes []uint8
		want  int
	}{
		{"two crashes ring together", []uint8{49, 49}, 2},
		{"the choke note ends them", []uint8{49, 49, 26}, 0},
		{"a hat cuts the open hat", []uint8{46, 42}, 1},
		{"an open hat cuts the last one", []uint8{46, 46}, 1},
		{"a pedal hat cuts the open hat", []uint8{46, 44}, 1},
		{"a kick leaves the open hat", []uint8{46, 36}, 2},
		{"a snare leaves the crash", []uint8{49, 38}, 2},
		{"the choke note leaves the hats", []uint8{46, 26}, 1},
	} {
		if got := play(test.notes...); got != test.want {
			t.Errorf("%s: %d voices, want %d", test.name, got, test.want)
		}
	}
}

func TestKitStarterScoreRendersEndToEnd(t *testing.T) {
	pack, _, recipe, _ := buildSession(t)
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(dir, "instrument.cicada"))
	if err != nil {
		t.Fatal(err)
	}
	score, diagnostics := notation.Parse(source)
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatalf("%s\n%s", d.Error(), source)
		}
	}
	if len(score.Samplers) != 1 || score.Samplers[0].Pack != "manifest.json" || score.Samplers[0].SHA256 != pack.Pin || len(score.Tracks) != 1 {
		t.Fatalf("starter must declare the pinned pack and one track: %+v", score)
	}
	mapped := map[int]bool{}
	for _, piece := range recipe.Pieces {
		mapped[piece.Note] = true
	}
	type start struct{ note, sample int }
	var starts []start
	var out bytes.Buffer
	report, err := render.WAV(score, render.Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Bars: 2, TailSec: 1, NoteTap: func(track int, event seq.Event) {
		if event.Kind == seq.NoteOn {
			starts = append(starts, start{int(event.Note), int(event.Sample)})
		}
	}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if report.OutputPeak < .05 || report.OutputPeak > .99 || report.ClippedSamples != 0 {
		t.Fatalf("starter render peak %g with %d clipped samples", report.OutputPeak, report.ClippedSamples)
	}
	// Tempo 100 makes a sixteenth 7,200 frames. The groove opens on the kick,
	// puts the snare on the second beat and never plays an unmapped note.
	played := map[int]bool{}
	for _, s := range starts {
		if !mapped[s.note] {
			t.Errorf("the starter plays note %d, which the kit lacks", s.note)
		}
		played[s.note] = true
		if s.sample%7200 != 0 {
			t.Errorf("note %d starts at frame %d, off the sixteenth grid", s.note, s.sample)
		}
	}
	if len(starts) < 12 || starts[0] != (start{36, 0}) {
		t.Fatalf("starter starts: %v", starts)
	}
	for _, s := range starts {
		if s.note == 38 {
			if s.sample != 4*7200 {
				t.Errorf("first snare at frame %d, want %d", s.sample, 4*7200)
			}
			break
		}
	}
	for _, note := range []int{36, 38, 42, 46} {
		if !played[note] {
			t.Errorf("the starter never plays note %d", note)
		}
	}
	// The open hat that ends bar one is followed by a closed hat before it
	// has rung out, so the starter exercises the choke.
	cut := false
	for i, s := range starts {
		for _, next := range starts[i+1:] {
			if s.note == 46 && next.note == 42 && next.sample-s.sample < 48000 {
				cut = true
			}
		}
	}
	if !cut {
		t.Error("the starter never follows its open hat with a closed hat")
	}
	audio, err := DecodeWAV(out.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got := rms(audio.PCM, 0, .05); got < .01 {
		t.Errorf("no kick in the first 50 ms: rms %g", got)
	}
}

// A kit without kick, snare and hat still gets a score that plays each piece.
func TestKitStarterFallsBackToATourOfThePieces(t *testing.T) {
	takes := fstest.MapFS{
		"floor.wav": {Data: testwav.Take(48000, testwav.Kick, .5, .52, .51)},
		"rack.wav":  {Data: testwav.Take(48000, testwav.Snare, .5, .52, .51)},
	}
	recipe := KitRecipe{Format: KitFormat, Pieces: []KitPiece{
		{Name: "floor_tom", Note: 41, Takes: []string{"floor.wav"}, Layers: 1, OneShot: true},
		{Name: "rack_tom", Note: 48, Takes: []string{"rack.wav"}, Layers: 1, OneShot: true},
	}}
	pack, _, err := BuildKit("toms", recipe, takes)
	if err != nil {
		t.Fatal(err)
	}
	source := string(pack.Files["instrument.cicada"])
	if !strings.Contains(source, "pattern groove notes {\n  f2 . . . c3 . . .\n}") || !strings.Contains(source, "root = f2") {
		t.Fatalf("starter:\n%s", source)
	}
	score, diagnostics := notation.Parse([]byte(source))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatalf("%s\n%s", d.Error(), source)
		}
	}
	dir := filepath.Join(t.TempDir(), "kit")
	if err := pack.Write(dir); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if report, err := render.WAV(score, render.Options{AssetDir: dir, SampleRate: 48000, Bits: 32, Bars: 1, TailSec: .5}, &out); err != nil || report.OutputPeak < .05 {
		t.Fatalf("render: %v, peak %g", err, report.OutputPeak)
	}
}
