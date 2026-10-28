package recording

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"path"
	"reflect"
	"slices"
	"sort"
	"strings"

	"m31labs.dev/cicada/audioasset"
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
// misspelled option cannot be ignored, and so are a field written twice and a
// field name with capitals, which the JSON decoder would take for the field
// anyway. Layers defaults to 3 and one_shot to true. A UTF-8 byte order mark,
// which some Windows editors add, is skipped.
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
		return recipe, recipeSyntax(data, raw, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return recipe, fmt.Errorf("unexpected text after the recipe's closing brace")
	}
	if err := checkRecipeKeys(data); err != nil {
		return recipe, err
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

func recipeSyntax(data []byte, raw rawKitRecipe, err error) error {
	var syntax *json.SyntaxError
	var typed *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syntax):
		return fmt.Errorf("line %d: %s", lineOf(data, syntax.Offset), syntax.Error())
	case errors.As(err, &typed):
		return recipeTypeError(data, raw, typed)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("the recipe is empty or ends early; it must be one JSON object")
	}
	return fmt.Errorf("%s", strings.TrimPrefix(err.Error(), "json: "))
}

// recipeTypeError says which value of the recipe has the wrong type, and where.
//
// encoding/json names the place in typed.Field, but how it does so depends on
// the Go version: from Go 1.27 the path holds the position of each list entry
// ("pieces.0.takes"), and earlier versions leave it out ("pieces.takes"). The
// message must read the same on both, so it uses only the field names, with
// the position dropped, and finds the piece from the error's offset, which
// every version reports alike.
func recipeTypeError(data []byte, raw rawKitRecipe, typed *json.UnmarshalTypeError) error {
	line := lineOf(data, typed.Offset)
	var path []string
	for _, step := range strings.Split(typed.Field, ".") {
		if strings.Trim(step, "0123456789") != "" {
			path = append(path, fieldName(step))
		}
	}
	want := typed.Type.String()
	switch typed.Type.Kind() {
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
	switch {
	case len(path) == 0:
		return fmt.Errorf("line %d: the recipe should be an object, not %s", line, typed.Value)
	case path[0] != "pieces":
		return fmt.Errorf("line %d: %q should be %s, not %s", line, path[0], want, typed.Value)
	}
	label := ""
	if index, found := pieceAt(data, typed.Offset); found {
		name := ""
		if index < len(raw.Pieces) {
			name = raw.Pieces[index].Name
		}
		label = pieceLabel(index, name)
	}
	switch {
	case len(path) == 1 && typed.Type.Kind() == reflect.Struct:
		// An entry of the list that is not an object has no fields to name.
		if label == "" {
			return fmt.Errorf("line %d: every entry of %q should be an object, not %s", line, "pieces", typed.Value)
		}
		return fmt.Errorf("line %d: %s should be an object, not %s", line, label, typed.Value)
	case len(path) == 1:
		return fmt.Errorf("line %d: %q should be %s, not %s", line, "pieces", want, typed.Value)
	}
	where := ""
	if label != "" {
		where = label + ": "
	}
	if path[1] == "takes" && typed.Type.Kind() == reflect.String {
		// The list is fine; one of its entries is not text.
		return fmt.Errorf("line %d: %severy entry of %q should be %s, not %s", line, where, path[1], want, typed.Value)
	}
	return fmt.Errorf("line %d: %s%q should be %s, not %s", line, where, path[1], want, typed.Value)
}

// fieldName gives the recipe's own spelling of a field. The decoder accepts any
// capitals in a key, and Go 1.27 names the field as the recipe spells it, where
// earlier versions use ours; checkRecipeKeys refuses the capitals later.
func fieldName(step string) string {
	for _, field := range slices.Concat(recipeFields, pieceFields) {
		if strings.EqualFold(field, step) {
			return field
		}
	}
	return step
}

// pieceAt finds which entry of the recipe's "pieces" list holds the byte before
// offset, counting from zero. It reads the recipe as tokens, as checkRecipeKeys
// does, so it does not depend on how a Go version words its errors.
func pieceAt(data []byte, offset int64) (int, bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if first, err := dec.Token(); err != nil || first != json.Delim('{') {
		return 0, false
	}
	var skip json.RawMessage
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return 0, false
		}
		name, _ := key.(string)
		if !strings.EqualFold(name, "pieces") || !listFollows(data, dec.InputOffset()) {
			if err := dec.Decode(&skip); err != nil {
				return 0, false
			}
			continue
		}
		if _, err := dec.Token(); err != nil { // the "["
			return 0, false
		}
		opened := dec.InputOffset()
		var starts []int64
		for dec.More() {
			starts = append(starts, dec.InputOffset())
			if err := dec.Decode(&skip); err != nil {
				return 0, false
			}
		}
		if _, err := dec.Token(); err != nil { // the "]"
			return 0, false
		}
		if offset <= opened || offset > dec.InputOffset() {
			continue // a "pieces" written twice: look at the next one
		}
		for index := len(starts) - 1; index >= 0; index-- {
			if starts[index] < offset {
				return index, true
			}
		}
	}
	return 0, false
}

// listFollows reports whether the value that starts after a key, which ends at
// pos, is a list.
func listFollows(data []byte, pos int64) bool {
	for ; pos < int64(len(data)); pos++ {
		switch data[pos] {
		case ' ', '\t', '\r', '\n', ':':
		default:
			return data[pos] == '['
		}
	}
	return false
}

func lineOf(data []byte, offset int64) int {
	offset = max(0, min(offset, int64(len(data))))
	return 1 + bytes.Count(data[:offset], []byte("\n"))
}

// The fields of the recipe and of each piece.
var (
	recipeFields = []string{"format", "pieces"}
	pieceFields  = []string{"name", "note", "takes", "layers", "choke", "choke_note", "one_shot", "gain_db"}
)

// checkRecipeKeys reads the recipe once more as a stream of tokens and refuses
// what encoding/json accepts quietly: a field written twice, where the last
// would win, and a field spelled with other capitals than ours. The recipe has
// already decoded, so its syntax and types are known to be sound.
func checkRecipeKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	// skip reads one value of any kind, to its end.
	skip := func() error {
		depth := 0
		for {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			if d, ok := tok.(json.Delim); ok {
				if d == '{' || d == '[' {
					depth++
				} else {
					depth--
				}
			}
			if depth == 0 {
				return nil
			}
		}
	}
	// object reads the members of an object whose "{" has been read. Only the
	// recipe's "pieces" member is read further, for the fields of each piece.
	var object func(where string, fields []string) error
	object = func(where string, fields []string) error {
		seen := map[string]bool{}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := tok.(string)
			line := lineOf(data, dec.InputOffset())
			if !slices.Contains(fields, key) {
				if i := slices.IndexFunc(fields, func(f string) bool { return strings.EqualFold(f, key) }); i >= 0 {
					return fmt.Errorf("line %d: write %q in lower case, not %q", line, fields[i], key)
				}
			}
			if seen[key] {
				return fmt.Errorf("line %d: %q appears twice %s; keep one", line, key, where)
			}
			seen[key] = true
			if key != "pieces" || len(fields) != len(recipeFields) {
				if err := skip(); err != nil {
					return err
				}
				continue
			}
			open, err := dec.Token()
			if err != nil {
				return err
			}
			if open != json.Delim('[') {
				continue
			}
			for dec.More() {
				entry, err := dec.Token()
				if err != nil {
					return err
				}
				if entry != json.Delim('{') {
					continue
				}
				if err := object("in this piece", pieceFields); err != nil {
					return err
				}
			}
			if _, err := dec.Token(); err != nil { // the closing "]"
				return err
			}
		}
		_, err := dec.Token() // the closing "}"
		return err
	}
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return nil
	}
	return object("in the recipe", recipeFields)
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
			if problem := takePathProblem(take); problem != "" {
				return fmt.Errorf("%s: take %q must be a path inside the recipe's folder; %s", label, take, problem)
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

// takePathProblem says what is wrong with a take's path, or returns "" when it
// is a plain relative path with forward slashes.
func takePathProblem(take string) string {
	switch {
	case take == "":
		return "this one is empty"
	case take == ".":
		return "this one names the folder itself, not a file"
	case strings.ContainsAny(take, "\\\x00"):
		return `separate folders with "/", not "\"`
	case strings.HasPrefix(take, "/"):
		return `this one starts with "/"; write it relative to the recipe's folder`
	case fs.ValidPath(take):
		return ""
	}
	if cleaned := path.Clean(take); cleaned != take && fs.ValidPath(cleaned) {
		if strings.HasPrefix(take, "./") {
			return fmt.Sprintf(`write %q, without the leading "./"`, cleaned)
		}
		return fmt.Sprintf("write %q", cleaned)
	}
	return `it may not start with "/" or "./", contain "..", or have empty parts`
}

// KitLayerReport describes one velocity layer of a piece. MinDB and MaxDB are
// the first-40-ms levels of its quietest and loudest hit, in dBFS.
type KitLayerReport struct {
	Hits         int
	MinDB, MaxDB float64
}

// KitTakeReport describes what BuildKit found in one take. Levels are in dBFS
// of the take as recorded.
type KitTakeReport struct {
	// Name is the take as the recipe names it.
	Name string
	Hits int
	// Cut counts the hits that the next hit or the end of the take ended while
	// their tails were still more than 6 dB above QuietDB. CutLowDB and
	// CutHighDB are the lowest and highest levels the tails had at the cut.
	Cut                 int
	CutLowDB, CutHighDB float64
	// Clipped counts the hits that hold a run of samples at full scale.
	Clipped int
	// Merged counts the attacks that came within half a second of an earlier
	// hit and so play inside it.
	Merged int
	// NoQuiet is set when the take has no quiet stretch, and NoRoomTone when
	// it does not start with room tone: see TakeReport.
	NoQuiet, NoRoomTone bool
	// LoudestDB is the loudest 10 ms of the take, NoiseDB its quietest 200 ms
	// and QuietDB the level below which a tail counts as finished.
	LoudestDB, NoiseDB, QuietDB float64
}

// KitPieceReport describes what BuildKit found for one piece.
type KitPieceReport struct {
	Name      string
	Note      int
	Hits      int
	Layers    []KitLayerReport
	Choke     string
	ChokeNote int
	// Takes lists what was found in each take, in the recipe's order.
	Takes []KitTakeReport
}

// KitReport summarizes a build for the command's receipt.
type KitReport struct {
	Pieces []KitPieceReport
	Hits   int
	// Zones counts every zone in the map, silent choke notes included.
	Zones int
}

// Warnings lists, one plain sentence each, the takes that should be recorded
// again. It is empty when every take is clean. For each piece it names first
// the takes with no quiet stretch or no room tone and the attacks that merged,
// take by take, then the hits that were cut off and the hits that clipped, for
// the piece.
func (r KitReport) Warnings() []string {
	var out []string
	for _, piece := range r.Pieces {
		var cutTakes, clipTakes []string
		cut, clipped, hits := 0, 0, 0
		quiet := math.Inf(-1)
		low, high := math.Inf(1), math.Inf(-1)
		for _, take := range piece.Takes {
			hits += take.Hits
			switch {
			case take.NoQuiet:
				out = append(out, fmt.Sprintf("%s: %s has no quiet stretch (its quietest 200 ms is only %.0f dB below its loudest sound), so tails end where they fall %.0f dB below that sound and the softer hits may be missed; start every take with 3 seconds of silence, and play louder or closer to the microphone if it already does", piece.Name, take.Name, take.LoudestDB-take.NoiseDB, take.LoudestDB-take.QuietDB))
			case take.NoRoomTone:
				out = append(out, fmt.Sprintf("%s: %s does not start with room tone, so the room's noise was read from its quietest 200 ms, at %.0f dBFS; if a tail was still ringing there, every hit ends too soon; start every take with 3 seconds of silence", piece.Name, take.Name, take.NoiseDB))
			}
			if take.Merged > 0 {
				out = append(out, fmt.Sprintf("%s: %s holds %s within 0.5 s of an earlier hit; %s kept inside that hit, which plays as a double strike; leave 0.5 s or more between hits", piece.Name, take.Name, count(take.Merged, "attack", "attacks"), plural(take.Merged, "it was", "each was")))
			}
			if take.Cut > 0 {
				cut += take.Cut
				cutTakes = append(cutTakes, take.Name)
				quiet = max(quiet, take.QuietDB)
				low, high = min(low, take.CutLowDB), max(high, take.CutHighDB)
			}
			if take.Clipped > 0 {
				clipped += take.Clipped
				clipTakes = append(clipTakes, take.Name)
			}
		}
		if cut > 0 {
			levels := fmt.Sprintf("%.0f dBFS", low)
			if math.Round(low) != math.Round(high) {
				levels = fmt.Sprintf("%.0f to %.0f dBFS", low, high)
			}
			out = append(out, fmt.Sprintf("%s: %d of %s %s cut off while still ringing (%s), at %s where a finished tail is below %.0f dBFS; leave more time after each hit", piece.Name, cut, count(hits, "hit", "hits"), plural(cut, "was", "were"), strings.Join(cutTakes, ", "), levels, quiet))
		}
		if clipped > 0 {
			out = append(out, fmt.Sprintf("%s: %d of %s %s at full scale (%s); lower the input gain and record those takes again", piece.Name, clipped, count(hits, "hit", "hits"), plural(clipped, "clips", "clip"), strings.Join(clipTakes, ", ")))
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func count(n int, one, many string) string { return fmt.Sprintf("%d %s", n, plural(n, one, many)) }

// kitAnalysis adds the piece and asset to a hit's analysis record. No take file
// names are kept: Input is the take's position in the piece's list.
type kitAnalysis struct {
	Piece string `json:"piece"`
	Asset string `json:"asset"`
	// Clipped counts the frames of the hit that lie in a run of full-scale
	// samples.
	Clipped int `json:"clipped,omitempty"`
	Hit
}

// stable rounds a measurement to a millionth. The Go math library can end a
// logarithm in another last digit on another kind of processor, and the record
// is compared byte for byte before a pack folder is reused.
func stable(x float64) float64 {
	if r := math.Round(x*1e6) / 1e6; r != 0 {
		return r
	}
	return 0
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
	var clipped []int // each hit's clipped frames, in hits' order
	first := make([]int, len(recipe.Pieces)+1)
	pieceTakes := make([][]KitTakeReport, len(recipe.Pieces))
	for i, piece := range recipe.Pieces {
		inputs := make([]Audio, 0, len(piece.Takes))
		for _, take := range piece.Takes {
			audio, err := readKitTake(takes, take)
			if err != nil {
				return nil, report, fmt.Errorf("piece %q: take %q: %w", piece.Name, take, err)
			}
			inputs = append(inputs, audio)
		}
		if err := checkKitBudget(piece.Name, inputs); err != nil {
			return nil, report, err
		}
		found, notes, err := AnalyzeTakes(inputs, Options{Root: piece.Note, Layers: piece.Layers, Ringing: true})
		if err != nil {
			return nil, report, fmt.Errorf("piece %q: %w", piece.Name, err)
		}
		reports := make([]KitTakeReport, len(piece.Takes))
		for n, name := range piece.Takes {
			reports[n] = KitTakeReport{Name: name, Merged: notes[n].Merged, NoQuiet: notes[n].NoQuiet, NoRoomTone: notes[n].NoRoomTone, LoudestDB: notes[n].LoudestDB, NoiseDB: notes[n].NoiseDB, QuietDB: notes[n].QuietDB}
		}
		for _, h := range found {
			// Each piece plays at its recorded pitch, so the pack must resample
			// at 44.1, 48 and 96 kHz output within the sampler's ratio limits.
			if !supportedRatio(piece.Note, h) {
				return nil, report, fmt.Errorf("piece %q: take %q is sampled at %d Hz, too low to play at every output rate; record at 44.1 kHz or higher", piece.Name, piece.Takes[h.Input], h.Rate)
			}
			take := &reports[h.Input]
			take.Hits++
			frames := inputs[h.Input].ClippedFrames(h.Start, h.End)
			clipped = append(clipped, frames)
			if frames > 0 {
				take.Clipped++
			}
			if h.Cut {
				if take.Cut == 0 {
					take.CutLowDB, take.CutHighDB = h.CutDB, h.CutDB
				}
				take.Cut++
				take.CutLowDB, take.CutHighDB = min(take.CutLowDB, h.CutDB), max(take.CutHighDB, h.CutDB)
			}
		}
		for _, take := range reports {
			if take.Hits == 0 {
				return nil, report, fmt.Errorf("piece %q: take %q has no hits above the noise floor; play it louder or closer to the microphone, or remove it from the recipe", piece.Name, take.Name)
			}
		}
		if len(found) < piece.Layers {
			return nil, report, fmt.Errorf("piece %q: found %d hits, but %d velocity layers need at least %d; record more hits or lower \"layers\"", piece.Name, len(found), piece.Layers, piece.Layers)
		}
		first[i] = len(hits)
		hits = append(hits, found...)
		pieceTakes[i] = reports
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
		summary := KitPieceReport{Name: piece.Name, Note: piece.Note, Hits: len(order), Choke: piece.Choke, ChokeNote: piece.ChokeNote, Takes: pieceTakes[i]}
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
			h := hits[n]
			h.LoudnessDB, h.TuneCents, h.CutDB = stable(h.LoudnessDB), stable(h.TuneCents), stable(h.CutDB)
			records[n] = kitAnalysis{Piece: piece.Name, Asset: ids[n], Clipped: clipped[n], Hit: h}
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
		if strings.Contains(err.Error(), "escapes from parent") {
			return Audio{}, fmt.Errorf("%w; a take cannot be a link to a file outside the recipe's folder", err)
		}
		return Audio{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if err != nil {
		return Audio{}, err
	}
	if h, err := audioasset.ReadWAVHeader(bytes.NewReader(data)); err == nil && h.Frames > MaxFrames {
		return Audio{}, fmt.Errorf("the file holds %.0f s of audio (%d frames at %d Hz), more than the %d frames a piece allows in all its takes (%.0f s at 44.1 kHz, %.0f s at 48 kHz); record fewer hits, or cut the silence out of it", float64(h.Frames)/float64(h.RateHz), h.Frames, h.RateHz, MaxFrames, float64(MaxFrames)/44100, float64(MaxFrames)/48000)
	}
	return DecodeWAV(data)
}

// checkKitBudget refuses a piece whose takes together hold more frames than a
// piece may. It says how much they hold and what to do, where Analyze can only
// say that a limit was passed.
func checkKitBudget(piece string, inputs []Audio) error {
	frames, seconds := 0, 0.0
	for _, a := range inputs {
		frames += len(a.PCM)
		seconds += float64(len(a.PCM)) / float64(max(1, a.Rate))
	}
	if frames <= MaxFrames {
		return nil
	}
	return fmt.Errorf("piece %q: the takes hold %.0f s of audio (%d frames), more than the %d frames a piece allows in all its takes (%.0f s at 44.1 kHz, %.0f s at 48 kHz); record fewer hits per dynamic, or cut the silence out of the takes", piece, seconds, frames, MaxFrames, float64(MaxFrames)/44100, float64(MaxFrames)/48000)
}
