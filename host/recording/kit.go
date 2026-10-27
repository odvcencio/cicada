package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"reflect"
	"sort"
	"strings"

	"m31labs.dev/cicada/host/instrumentpack"
)

// KitFormat is the version tag every kit recipe carries.
const KitFormat = "cicada.kit-recipe/1"

const (
	// MaxKitPieces bounds one recipe. A sampler pack allows 255 key groups and
	// each piece uses one, plus one more for a silent choke note.
	MaxKitPieces = 64
	// MaxKitTakes is the per-piece WAV file limit, the same as record-pack.
	MaxKitTakes = 32
	// MaxKitRecipeBytes bounds the recipe file.
	MaxKitRecipeBytes = 1 << 20
	// The sampler declaration's root accepts C0 to B6, so kit notes do too.
	MinKitNote = 12
	MaxKitNote = 95
	// DefaultKitLayers matches record-pack.
	DefaultKitLayers = 3
	// MinKitGainDB and MaxKitGainDB bound a piece's level trim.
	MinKitGainDB = -24.0
	MaxKitGainDB = 12.0
)

// KitPiece is one drum or cymbal of a kit.
//
// Takes name WAV files relative to the recipe's folder. The piece plays on the
// single MIDI note Note. Hits sort by their first 40 ms of loudness into
// Layers velocity layers; hits in a layer are round robins.
//
// Choke names a group of pieces that cut each other off: any hit in the group
// damps the group's older hits, which is how a closed or pedal hi-hat ends an
// open one. ChokeNote instead lets the piece's own hits ring together (a
// cymbal) and makes that note a silent control that damps them. A piece uses
// one or the other.
//
// OneShot plays each hit through, ignoring note length. GainDB trims the piece
// against the rest of the kit.
type KitPiece struct {
	Name      string   `json:"name"`
	Note      int      `json:"note"`
	Takes     []string `json:"takes"`
	Layers    int      `json:"layers"`
	Choke     string   `json:"choke,omitempty"`
	ChokeNote int      `json:"choke_note,omitempty"`
	OneShot   bool     `json:"one_shot"`
	GainDB    float64  `json:"gain_db,omitempty"`
}

// KitRecipe lists the pieces of one kit, in the order they appear in the pack.
type KitRecipe struct {
	Format string     `json:"format"`
	Pieces []KitPiece `json:"pieces"`
}

// The decoding forms use pointers so that a missing field differs from zero.
type rawKitRecipe struct {
	Format string        `json:"format"`
	Pieces []rawKitPiece `json:"pieces"`
}

type rawKitPiece struct {
	Name      string   `json:"name"`
	Note      *int     `json:"note"`
	Takes     []string `json:"takes"`
	Layers    *int     `json:"layers"`
	Choke     string   `json:"choke"`
	ChokeNote *int     `json:"choke_note"`
	OneShot   *bool    `json:"one_shot"`
	GainDB    float64  `json:"gain_db"`
}

// ParseKitRecipe reads and validates a recipe. Unknown fields are refused so a
// misspelled option cannot be ignored. Layers defaults to 3 and one_shot to
// true. A UTF-8 byte order mark, which some Windows editors add, is skipped.
func ParseKitRecipe(data []byte) (KitRecipe, error) {
	var recipe KitRecipe
	if len(data) > MaxKitRecipeBytes {
		return recipe, fmt.Errorf("the recipe is larger than %d bytes", MaxKitRecipeBytes)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var raw rawKitRecipe
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return recipe, recipeSyntax(data, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return recipe, fmt.Errorf("unexpected text after the recipe's closing brace")
	}
	recipe.Format = raw.Format
	for i, p := range raw.Pieces {
		piece := KitPiece{Name: p.Name, Takes: p.Takes, Choke: p.Choke, GainDB: p.GainDB, Layers: DefaultKitLayers, OneShot: true}
		label := pieceLabel(i, p.Name)
		if p.Note == nil {
			return recipe, fmt.Errorf(`%s: "note" is missing; give the MIDI note this piece plays, %d to %d`, label, MinKitNote, MaxKitNote)
		}
		piece.Note = *p.Note
		if p.Layers != nil {
			piece.Layers = *p.Layers
		}
		if p.ChokeNote != nil {
			if *p.ChokeNote < MinKitNote || *p.ChokeNote > MaxKitNote {
				return recipe, fmt.Errorf(`%s: "choke_note" %d is outside %d to %d`, label, *p.ChokeNote, MinKitNote, MaxKitNote)
			}
			piece.ChokeNote = *p.ChokeNote
		}
		if p.OneShot != nil {
			piece.OneShot = *p.OneShot
		}
		recipe.Pieces = append(recipe.Pieces, piece)
	}
	if err := recipe.Validate(); err != nil {
		return KitRecipe{}, err
	}
	return recipe, nil
}

func recipeSyntax(data []byte, err error) error {
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Errorf("line %d: %s", lineOf(data, syntax.Offset), syntax.Error())
	case errors.As(err, &typed):
		line := lineOf(data, typed.Offset)
		want := typed.Type.String()
		switch typed.Type.Kind() {
		case reflect.Struct:
			if typed.Field == "" {
				return fmt.Errorf("line %d: the recipe should be an object, not %s", line, typed.Value)
			}
			return fmt.Errorf("line %d: every entry of %q should be an object, not %s", line, typed.Field, typed.Value)
		case reflect.Int:
			want = "a whole number"
		case reflect.String:
			want = "text"
		case reflect.Bool:
			want = "true or false"
		case reflect.Float64:
			want = "a number"
		case reflect.Slice:
			want = "a list"
		}
		return fmt.Errorf("line %d: %q should be %s, not %s", line, typed.Field, want, typed.Value)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("the recipe is empty or ends early; it must be one JSON object")
	}
	return fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "json: "))
}

func lineOf(data []byte, offset int64) int {
	offset = max(0, min(offset, int64(len(data))))
	return 1 + bytes.Count(data[:offset], []byte("\n"))
}

func pieceLabel(index int, name string) string {
	if name == "" {
		return fmt.Sprintf("piece %d", index+1)
	}
	return fmt.Sprintf("piece %d (%s)", index+1, name)
}

// reservedSamplerName reports names a score refuses for a sampler.
func reservedSamplerName(name string) bool {
	switch name {
	case "acid", "drums", "guitar", "audio":
		return true
	}
	return false
}

// Validate checks a recipe without reading any audio. ParseKitRecipe calls it;
// call it directly on a recipe built in Go.
func (r KitRecipe) Validate() error {
	if r.Format != KitFormat {
		return fmt.Errorf(`"format" must be %q`, KitFormat)
	}
	if len(r.Pieces) < 1 || len(r.Pieces) > MaxKitPieces {
		return fmt.Errorf(`"pieces" must list 1 to %d pieces`, MaxKitPieces)
	}
	names := map[string]bool{}
	notes := map[int]string{}
	claim := func(note int, owner string) error {
		if previous, taken := notes[note]; taken {
			return fmt.Errorf("%s and %s both use note %d (%s); give each its own note", previous, owner, note, NoteName(note))
		}
		notes[note] = owner
		return nil
	}
	for i, piece := range r.Pieces {
		label := pieceLabel(i, piece.Name)
		if !ValidName(piece.Name) {
			return fmt.Errorf(`%s: "name" must be lowercase letters, digits and underscores, start with a letter and have at most 48 characters`, label)
		}
		if names[piece.Name] {
			return fmt.Errorf("%s: another piece already has this name", label)
		}
		names[piece.Name] = true
		if piece.Note < MinKitNote || piece.Note > MaxKitNote {
			return fmt.Errorf(`%s: "note" %d is outside %d to %d`, label, piece.Note, MinKitNote, MaxKitNote)
		}
		if err := claim(piece.Note, fmt.Sprintf("piece %q", piece.Name)); err != nil {
			return err
		}
		if len(piece.Takes) < 1 || len(piece.Takes) > MaxKitTakes {
			return fmt.Errorf(`%s: "takes" must list 1 to %d WAV files`, label, MaxKitTakes)
		}
		seen := map[string]bool{}
		for _, take := range piece.Takes {
			if take == "" || !fs.ValidPath(take) || take == "." || strings.ContainsAny(take, "\\\x00") {
				return fmt.Errorf(`%s: take %q must be a path inside the recipe's folder, with forward slashes and no leading "/" or ".."`, label, take)
			}
			if seen[take] {
				return fmt.Errorf("%s: take %q is listed twice; each hit would count twice", label, take)
			}
			seen[take] = true
		}
		if piece.Layers < 1 || piece.Layers > 8 {
			return fmt.Errorf(`%s: "layers" %d is outside 1 to 8`, label, piece.Layers)
		}
		if piece.Choke != "" && !ValidName(piece.Choke) {
			return fmt.Errorf(`%s: "choke" must be a lowercase group name such as "hat"`, label)
		}
		if piece.Choke != "" && piece.ChokeNote != 0 {
			return fmt.Errorf(`%s: use either "choke" or "choke_note", not both`, label)
		}
		if piece.ChokeNote != 0 {
			if piece.ChokeNote < MinKitNote || piece.ChokeNote > MaxKitNote {
				return fmt.Errorf(`%s: "choke_note" %d is outside %d to %d`, label, piece.ChokeNote, MinKitNote, MaxKitNote)
			}
			if err := claim(piece.ChokeNote, fmt.Sprintf("the choke note of piece %q", piece.Name)); err != nil {
				return err
			}
		}
		if math.IsNaN(piece.GainDB) || piece.GainDB < MinKitGainDB || piece.GainDB > MaxKitGainDB {
			return fmt.Errorf(`%s: "gain_db" %g is outside %g to %g`, label, piece.GainDB, MinKitGainDB, MaxKitGainDB)
		}
	}
	return nil
}

// KitLayerReport describes one velocity layer of a piece. MinDB and MaxDB are
// the first-40-ms levels of its quietest and loudest hit, in dBFS.
type KitLayerReport struct {
	Hits         int
	MinDB, MaxDB float64
}

// KitPieceReport describes what BuildKit found for one piece.
type KitPieceReport struct {
	Name      string
	Note      int
	Hits      int
	Layers    []KitLayerReport
	Choke     string
	ChokeNote int
}

// KitReport summarizes a build for the command's receipt.
type KitReport struct {
	Pieces []KitPieceReport
	Hits   int
	// Zones counts every zone in the map, silent choke notes included.
	Zones int
}

// kitAnalysis adds the piece and asset to a hit's analysis record. No take file
// names are kept: Input is the take's position in the piece's list.
type kitAnalysis struct {
	Piece string `json:"piece"`
	Asset string `json:"asset"`
	Hit
}

const kitPCMLimit = instrumentpack.MaxPCMBytes

// BuildKit analyzes each piece's takes and returns one pack for the whole kit.
// takes resolves the recipe's file names. Nothing is written; use Pack.Write.
//
// Each piece maps to exactly its note. The kit keeps the levels it was
// recorded at: the loudest hit anywhere in the kit plays at full scale and
// every other hit keeps its recorded level against it, then the piece's
// GainDB applies. Every asset carries the "owner recording" label.
func BuildKit(name string, recipe KitRecipe, takes fs.FS) (*Pack, KitReport, error) {
	var report KitReport
	if !ValidName(name) || reservedSamplerName(name) {
		return nil, report, fmt.Errorf("use a lowercase kit name of letters, digits and underscores; acid, drums, guitar and audio are reserved by scores")
	}
	if err := recipe.Validate(); err != nil {
		return nil, report, err
	}
	var hits []Hit
	first := make([]int, len(recipe.Pieces)+1)
	for i, piece := range recipe.Pieces {
		inputs := make([]Audio, 0, len(piece.Takes))
		for _, take := range piece.Takes {
			audio, err := readKitTake(takes, take)
			if err != nil {
				return nil, report, fmt.Errorf("piece %q: take %q: %w", piece.Name, take, err)
			}
			inputs = append(inputs, audio)
		}
		found, err := Analyze(inputs, Options{Root: piece.Note, Layers: piece.Layers, Ringing: true})
		if err != nil {
			return nil, report, fmt.Errorf("piece %q: %w", piece.Name, err)
		}
		perTake := make([]int, len(piece.Takes))
		for _, h := range found {
			perTake[h.Input]++
			// Each piece plays at its recorded pitch, so the pack must resample
			// at 44.1, 48 and 96 kHz output within the sampler's ratio limits.
			if !supportedRatio(piece.Note, h) {
				return nil, report, fmt.Errorf("piece %q: take %q is sampled at %d Hz, too low to play at every output rate; record at 44.1 kHz or higher", piece.Name, piece.Takes[h.Input], h.Rate)
			}
		}
		for n, count := range perTake {
			if count == 0 {
				return nil, report, fmt.Errorf("piece %q: take %q has no hits above the noise floor; play it louder or closer to the microphone, or remove it from the recipe", piece.Name, piece.Takes[n])
			}
		}
		if len(found) < piece.Layers {
			return nil, report, fmt.Errorf("piece %q: found %d hits, but %d velocity layers need at least %d; record more hits or lower \"layers\"", piece.Name, len(found), piece.Layers, piece.Layers)
		}
		first[i] = len(hits)
		hits = append(hits, found...)
	}
	first[len(recipe.Pieces)] = len(hits)
	var frames int64
	maxPeak := 0.0
	for _, h := range hits {
		frames += int64(len(h.PCM))
		maxPeak = max(maxPeak, h.Peak)
	}
	if frames*4 > kitPCMLimit {
		return nil, report, fmt.Errorf("the kit holds %d MiB of audio after trimming, over the %d MiB a pack allows; trim the ring-outs or record fewer hits", frames*4>>20, int64(kitPCMLimit)>>20)
	}
	pack := &Pack{Hits: hits, Files: map[string][]byte{}, Manifest: newManifest(name, "Owner recording; drum kit with velocity layers, round robins and choke groups")}
	ids := make([]string, len(hits))
	for i, piece := range recipe.Pieces {
		for n := first[i]; n < first[i+1]; n++ {
			ids[n] = fmt.Sprintf("%s_%03d", piece.Name, n-first[i]+1)
			data, asset, err := encodeHit(ids[n], hits[n], "owner recording")
			if err != nil {
				return nil, report, err
			}
			pack.Files[asset.Path] = data
			pack.Manifest.Assets = append(pack.Manifest.Assets, asset)
		}
	}
	// Choke group numbers follow the order pieces appear: named groups at the
	// first piece that uses them, a private group for each choke note.
	var groups []string
	chokeGroup := make([]int, len(recipe.Pieces))
	for i, piece := range recipe.Pieces {
		switch {
		case piece.Choke != "":
			at := -1
			for g, existing := range groups {
				if existing == piece.Choke {
					at = g
				}
			}
			if at < 0 {
				groups = append(groups, piece.Choke)
				at = len(groups) - 1
			}
			chokeGroup[i] = at + 1
		case piece.ChokeNote != 0:
			groups = append(groups, "")
			chokeGroup[i] = len(groups)
		}
	}
	nextControl := len(recipe.Pieces) + 1
	for i, piece := range recipe.Pieces {
		order := make([]int, 0, first[i+1]-first[i])
		for n := first[i]; n < first[i+1]; n++ {
			order = append(order, n)
		}
		sort.SliceStable(order, func(a, b int) bool { return hits[order[a]].LoudnessDB < hits[order[b]].LoudnessDB })
		trim := math.Round(math.Pow(10, piece.GainDB/20)*1e6) / 1e6
		summary := KitPieceReport{Name: piece.Name, Note: piece.Note, Hits: len(order), Choke: piece.Choke, ChokeNote: piece.ChokeNote}
		start := len(pack.Manifest.Zones)
		for layer := 0; layer < piece.Layers; layer++ {
			from, to := layer*len(order)/piece.Layers, (layer+1)*len(order)/piece.Layers
			if to-from > 32 {
				return nil, report, fmt.Errorf("piece %q: more than 32 hits in velocity layer %d; use more layers", piece.Name, layer+1)
			}
			vlow, vhigh := 1+layer*127/piece.Layers, (layer+1)*127/piece.Layers
			layerReport := KitLayerReport{Hits: to - from, MinDB: hits[order[from]].LoudnessDB, MaxDB: hits[order[to-1]].LoudnessDB}
			summary.Layers = append(summary.Layers, layerReport)
			for position, n := range order[from:to] {
				pack.Manifest.Zones = append(pack.Manifest.Zones, Zone{
					Asset: ids[n], Root: piece.Note, KeyLow: piece.Note, KeyHigh: piece.Note,
					VelocityLow: vlow, VelocityHigh: vhigh, Layer: (vlow + vhigh) / 2,
					Group: i + 1, Position: position, Count: to - from, End: len(hits[n].PCM),
					Gain: hits[n].Peak / maxPeak * trim, OneShot: piece.OneShot,
					ChokeGroup: chokeGroup[i], ChokeSustain: piece.ChokeNote != 0,
				})
			}
		}
		if piece.ChokeNote != 0 {
			// A silent zone of the piece's own group: it plays nothing and
			// damps the group, which holds every earlier hit of the piece.
			control := pack.Manifest.Zones[start]
			control.Root, control.KeyLow, control.KeyHigh = piece.ChokeNote, piece.ChokeNote, piece.ChokeNote
			control.VelocityLow, control.VelocityHigh, control.Layer = 1, 127, 64
			control.Group, control.Position, control.Count = nextControl, 0, 1
			control.End = min(control.End, 96)
			control.Gain, control.OneShot, control.ChokeSustain = 0, true, false
			pack.Manifest.Zones = append(pack.Manifest.Zones, control)
			nextControl++
		}
		report.Pieces = append(report.Pieces, summary)
	}
	report.Hits, report.Zones = len(hits), len(pack.Manifest.Zones)
	manifest, err := json.MarshalIndent(pack.Manifest, "", "  ")
	if err != nil {
		return nil, report, err
	}
	manifest = append(manifest, '\n')
	if len(manifest) > instrumentpack.MaxManifestBytes {
		return nil, report, fmt.Errorf("the kit map is %d bytes, over the %d a pack allows; record fewer hits", len(manifest), instrumentpack.MaxManifestBytes)
	}
	pack.Files["manifest.json"] = manifest
	pack.Pin = Digest(manifest)
	records := make([]kitAnalysis, len(hits))
	for i, piece := range recipe.Pieces {
		for n := first[i]; n < first[i+1]; n++ {
			records[n] = kitAnalysis{Piece: piece.Name, Asset: ids[n], Hit: hits[n]}
		}
	}
	analysis, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return nil, report, err
	}
	pack.Files["analysis.json"] = append(analysis, '\n')
	pack.Files["LICENSE.txt"] = []byte("owner recording\nAudio comes from recordings made by the person who built this kit. This label records provenance; it grants no public redistribution rights.\n")
	pack.Files["instrument.cicada"] = []byte(kitStarter(pack, recipe))
	return pack, report, nil
}

// readKitTake reads one take, refusing anything over the input limit.
func readKitTake(takes fs.FS, name string) (Audio, error) {
	f, err := takes.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return Audio{}, fmt.Errorf("not found; takes are looked up next to the recipe")
	}
	if err != nil {
		return Audio{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil {
		return Audio{}, err
	}
	return DecodeWAV(data)
}
