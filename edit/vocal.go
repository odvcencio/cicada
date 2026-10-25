package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/musictime"
	"m31labs.dev/cicada/notation"
)

// VocalSegment is one take region inside a vocal comp. The JSON names are
// also the names in the comp's metadata comment, so they keep their spelling.
type VocalSegment struct {
	Take   string  `json:"take"`
	Start  int64   `json:"startTick"`
	End    int64   `json:"endTick"`
	GainDB float64 `json:"gainDB,omitempty"`
	// Positive offset reads later source frames at the same song position. Raw
	// take coordinates remain immutable; trimming either side keeps this mapping.
	OffsetMS    float64  `json:"offsetMS,omitempty"`
	FadeMS      *float64 `json:"fadeMS,omitempty"`
	CrossfadeMS *float64 `json:"crossfadeMS,omitempty"`
}

// VocalComp is the comp for one track: which take plays where, and the name of
// the printed asset. It is stored in the score as a `// cicada-vocal-comp`
// comment.
type VocalComp struct {
	Elastic      bool                    `json:"elastic,omitempty"`
	Track        string                  `json:"track"`
	Tempo        int64                   `json:"tempoMilli"`
	TempoChanges []musictime.TempoChange `json:"tempoChanges,omitempty"`
	Segments     []VocalSegment          `json:"segments"`
	Asset        string                  `json:"asset"`
}

// VocalCompPrefix starts the metadata comment line of a vocal comp.
const VocalCompPrefix = "// cicada-vocal-comp "

// PlaceVocalComp writes a printed vocal comp into the score: an asset, a clip
// and a placement on the track, between `cicada-vocal-begin` and
// `cicada-vocal-end` comments. When Previous is set, the comp's earlier print
// is removed first and only the managed block and the track's vocal placement
// change; every other byte stays. A song of scenes gains an arrange block of
// ordinary placements so one vocal can span its blocks. The host prints the
// audio (Asset names the file it wrote) and chooses the segments.
type PlaceVocalComp struct {
	Comp     VocalComp      `json:"comp"`
	Asset    PrintAsset     `json:"asset"`
	Previous *VocalPrevious `json:"previous,omitempty"`
}

// VocalPrevious names the print this comp replaces.
type VocalPrevious struct {
	Asset string `json:"asset"`
}

func (PlaceVocalComp) Kind() string { return "placevocalcomp" }

func init() {
	Register("placevocalcomp", func() Intent { return &PlaceVocalComp{} })
	Handle("placevocalcomp", func(ctx *Context, intent Intent) error {
		updated, err := vocalCompSource(ctx, *intent.(*PlaceVocalComp))
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel("Select audio take")
		return nil
	})
}

// ValidateVocalGain is the phrase gain rule: finite, -24 to +18 dB.
func ValidateVocalGain(gainDB float64) error {
	if math.IsNaN(gainDB) || math.IsInf(gainDB, 0) || gainDB < -24 || gainDB > 18 {
		return fmt.Errorf("phrase gain must be finite and between -24 and +18 dB")
	}
	return nil
}

// ValidateVocalEdits is the phrase timing rule: advance within 500 ms, fades within 0 to 100 ms.
func ValidateVocalEdits(offsetMS float64, fadeMS, crossfadeMS *float64) error {
	if math.IsNaN(offsetMS) || math.IsInf(offsetMS, 0) || offsetMS < -500 || offsetMS > 500 {
		return fmt.Errorf("phrase timing advance must be finite and between -500 and +500 ms")
	}
	for name, value := range map[string]*float64{"fade": fadeMS, "crossfade": crossfadeMS} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 || *value > 100) {
			return fmt.Errorf("phrase %s must be finite and between 0 and 100 ms", name)
		}
	}
	return nil
}

// ValidateVocalComp is the one rule for a comp's metadata. The intent applies
// it before writing the metadata comment and Studio applies it when reading
// the comment back, so a comp that Studio would reject is never written.
func ValidateVocalComp(c VocalComp) error {
	if err := musictime.ValidateTempoChanges(c.TempoChanges); err != nil {
		return fmt.Errorf("invalid vocal comp tempo map: %w", err)
	}
	if !patternName.MatchString(c.Track) || len(c.Segments) > 4096 || c.Tempo < 20000 || c.Tempo > 300000 {
		return fmt.Errorf("invalid vocal comp metadata")
	}
	for _, seg := range c.Segments {
		if seg.Start < 0 || seg.End <= seg.Start || seg.End > 1<<40 {
			return fmt.Errorf("invalid vocal comp region")
		}
		if err := ValidateVocalGain(seg.GainDB); err != nil {
			return fmt.Errorf("invalid vocal comp metadata: %w", err)
		}
		if err := ValidateVocalEdits(seg.OffsetMS, seg.FadeMS, seg.CrossfadeMS); err != nil {
			return fmt.Errorf("invalid vocal comp metadata: %w", err)
		}
	}
	return nil
}

var printedName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,127}$`)

// validatePrintAsset checks the file facts that become an asset declaration.
func validatePrintAsset(name string, a PrintAsset) error {
	if !printedName.MatchString(name) {
		return fmt.Errorf("choose a valid name for the printed audio")
	}
	if a.Path == "" || a.Frames <= 0 || a.RateHz <= 0 || len(a.SHA256) != 64 {
		return fmt.Errorf("printed audio needs a path, a SHA-256 digest, a frame count and a sample rate")
	}
	return nil
}

func vocalCompSource(ctx *Context, in PlaceVocalComp) ([]byte, error) {
	next, track := in.Comp, in.Comp.Track
	if len(next.Segments) == 0 {
		return nil, fmt.Errorf("choose a vocal phrase before printing")
	}
	if err := ValidateVocalComp(next); err != nil {
		return nil, err
	}
	if err := validatePrintAsset(next.Asset, in.Asset); err != nil {
		return nil, err
	}
	if in.Previous != nil && !printedName.MatchString(in.Previous.Asset) {
		return nil, fmt.Errorf("choose a valid name for the printed audio")
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, err
	}
	if int64(plan.TempoMilli) != next.Tempo || !slices.Equal(plan.TempoChanges, next.TempoChanges) {
		return nil, fmt.Errorf("vocal comp tempo differs from the project tempo")
	}
	source, file := ctx.Source, ctx.Options.Path
	var arrangement, original []byte
	owner := file
	if plan.HasArrange {
		arrangement, owner, err = arrangementFile(ctx)
		if err != nil {
			return nil, err
		}
		original = arrangement
	} else {
		score, ds, err := ctx.ParseProject()
		if err != nil {
			return nil, err
		}
		if score == nil || hasErrors(ds) || len(score.Song) == 0 {
			return nil, fmt.Errorf("full-song recording needs a valid song or arrangement")
		}
		if score.SongPosition.File != "" {
			owner = score.SongPosition.File
		}
		if err := carrySongAutomation(ctx, score, plan, owner); err != nil {
			return nil, err
		}
		source = ctx.Source
		var supplied bool
		original, supplied = ctx.FileSource(owner)
		if !supplied {
			return nil, fmt.Errorf("song is outside the editable project sources")
		}
		newline := "\n"
		if owner != file {
			newline = Newline(original)
		}
		arrangement, err = vocalArrangementSource(original, owner, plan, newline)
		if err != nil {
			return nil, err
		}
		if owner == file {
			source = arrangement
		}
	}
	// Remove just the managed block and its placement, preserving every other byte.
	if in.Previous != nil {
		previous := VocalComp{Track: track, Asset: in.Previous.Asset}
		a, end, begin, finish, err := vocalPrintBlock(source, track)
		if err != nil {
			return nil, err
		}
		// Generic score edits can append an effect or another declaration before
		// the trailing managed comment. Delete only our print declarations and
		// metadata, retaining those user/application edits byte for byte.
		block := source[a:end]
		kept, e := removeVocalPrintDeclarations(block, file, previous, begin, finish)
		if e != nil {
			return nil, e
		}
		source, err = ReplaceSpan(source, file, Span{a, end, string(kept), file})
		if err != nil {
			return nil, err
		}
	}
	if owner == file {
		arrangement = source
	}
	newline := Newline(arrangement)
	if owner == file {
		newline = "\n"
	} // Preserve the entry-only writer's output.
	updated, err := vocalPlacementSource(arrangement, owner, next, in.Previous != nil, newline, arrangementEnd(plan))
	if err != nil {
		return nil, err
	}
	if owner == file {
		source = updated
	} else {
		stageProjectSource(ctx, owner, original, updated)
	}
	asset := in.Asset
	metadata, _ := json.Marshal(next)
	text := fmt.Sprintf("\n// cicada-vocal-begin %s\n%s%s\nasset %s %s { sha256 = %s format = wav frames = %d rate = %dHz channels = 1 source = recorded }\nclip %s-clip %s { start = 0frames end = %dframes }\n// cicada-vocal-end %s\n", track, VocalCompPrefix, metadata, next.Asset, strconv.Quote(asset.Path), strconv.Quote(asset.SHA256), asset.Frames, asset.RateHz, next.Asset, next.Asset, asset.Frames, track)
	if owner != file {
		text = strings.ReplaceAll(text, "\n", Newline(source))
	}
	return append(source, []byte(text)...), nil
}

func vocalPlacementSource(source []byte, file string, next VocalComp, previous bool, newline string, minimumEnd int64) ([]byte, error) {
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var spans []Span
	foundArrangement := false
	track := next.Track
	placeID := "vocal-" + track
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) != "arrange_decl" {
			continue
		}
		foundArrangement = true
		var backingEnd int64
		for j := 0; j < n.NamedChildCount(); j++ {
			child := n.NamedChild(j)
			if w.Type(child) == "place_decl" && w.Text(w.Field(child, "name")) == placeID {
				if !previous {
					return nil, fmt.Errorf("placement name %s already exists", placeID)
				}
				spans = append(spans, Span{int(child.StartByte()), int(child.EndByte()), "", file})
			} else if w.Type(child) == "place_decl" {
				var at, length int64
				for k := 0; k < child.NamedChildCount(); k++ {
					param := child.NamedChild(k)
					if w.Type(param) != "param_decl" {
						continue
					}
					name, value := w.Text(w.Field(param, "name")), w.Text(w.Field(param, "value"))
					if name == "at" {
						at, err = notation.MusicalTicks(value, true)
					} else if name == "length" {
						length, err = notation.MusicalTicks(value, false)
					}
					if err != nil {
						return nil, err
					}
				}
				backingEnd = max(backingEnd, at+length)
			}
		}
		first, last := next.Segments[0].Start, next.Segments[len(next.Segments)-1].End
		// Audio regions stop at their source end; a longer placement retains a
		// quiet song ending without stretching or padding the recorded audio.
		if backingEnd < minimumEnd {
			last = max(last, minimumEnd)
		}
		line := fmt.Sprintf("%s  place %s %s %s-clip { at = %dticks length = %dticks }%s", newline, placeID, track, next.Asset, first, last-first, newline)
		spans = append(spans, Span{int(n.EndByte()) - 1, int(n.EndByte()) - 1, line, file})
	}
	if !foundArrangement {
		return nil, fmt.Errorf("open the score file containing the arrangement before selecting a vocal")
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start > spans[j].Start })
	return PatchSpans(source, file, spans)
}

// Locate whole managed marker lines in either current CRLF or legacy LF text.
func vocalPrintBlock(source []byte, track string) (start, end int, begin, finish []byte, err error) {
	start = -1
	for at := 0; at < len(source); {
		end = bytes.IndexByte(source[at:], '\n')
		if end < 0 {
			end = len(source)
		} else {
			end += at + 1
		}
		line := bytes.TrimSuffix(bytes.TrimSuffix(source[at:end], []byte("\n")), []byte("\r"))
		if start < 0 && string(line) == "// cicada-vocal-begin "+track {
			start, begin = at, bytes.Clone(source[at:end])
		} else if start >= 0 && string(line) == "// cicada-vocal-end "+track {
			return start, end, begin, bytes.Clone(source[at:end]), nil
		}
		at = end
	}
	if start < 0 {
		return 0, 0, nil, nil, fmt.Errorf("vocal comp declarations were edited; restore them before selecting")
	}
	return 0, 0, nil, nil, fmt.Errorf("vocal comp declarations are incomplete")
}

func removeVocalPrintDeclarations(block []byte, file string, previous VocalComp, begin, finish []byte) ([]byte, error) {
	root, w, err := notation.ParseTree(block)
	if err != nil {
		return nil, err
	}
	spans := []Span{{0, len(begin), "", file}, {len(block) - len(finish), len(block), "", file}}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		kind, name := w.Type(n), w.Text(w.Field(n, "name"))
		if kind == "asset_decl" && name == previous.Asset || kind == "clip_decl" && name == previous.Asset+"-clip" {
			spans = append(spans, Span{int(n.StartByte()), int(n.EndByte()), "", file})
		}
	}
	for at := 0; at < len(block); {
		end := bytes.IndexByte(block[at:], '\n')
		if end < 0 {
			end = len(block) - at - 1
		}
		end += at + 1
		if bytes.HasPrefix(block[at:end], []byte(VocalCompPrefix)) {
			var metadata VocalComp
			if json.Unmarshal(bytes.TrimSpace(block[at+len(VocalCompPrefix):end]), &metadata) == nil && metadata.Track == previous.Track {
				spans = append(spans, Span{at, end, "", file})
			}
		}
		at = end
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start > spans[j].Start })
	return PatchSpans(block, file, spans)
}

// vocalArrangementSource gives a song of scenes ordinary placements so a
// continuous vocal can span blocks. Explicit scene bindings restart a part;
// Keep carries its phase to the next block. A score that already has an
// arrange block is unchanged.
func vocalArrangementSource(source []byte, file string, plan *Plan, newline string) ([]byte, error) {
	if plan.HasArrange {
		return source, nil
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var start, end int
	var song *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) == "song_decl" {
			start, end = int(n.StartByte()), int(n.EndByte())
			song = n
			break
		}
	}
	if end == 0 {
		return nil, fmt.Errorf("full-song recording needs a song or arrangement")
	}
	scenes := map[string]Scene{}
	for _, v := range plan.Scenes {
		scenes[v.ID] = v
	}
	var text strings.Builder
	text.WriteString("arrange {\n")
	var comments func(*gts.Node)
	comments = func(node *gts.Node) {
		if w.Type(node) == "comment" {
			text.WriteString("  " + strings.TrimRight(w.Text(node), "\r\n") + "\n")
			return
		}
		for i := 0; i < node.NamedChildCount(); i++ {
			comments(node.NamedChild(i))
		}
	}
	comments(song)
	at := int64(0)
	counter := 0
	type activePart struct {
		content string
		start   int64
	}
	active := map[string]activePart{}
	flush := func(track string, part activePart, until int64) {
		if until <= part.start {
			return
		}
		counter++
		fmt.Fprintf(&text, "  place backing-%d %s %s { at = %dticks length = %dticks }\n", counter, track, part.content, part.start, until-part.start)
	}
	for i, entry := range plan.Song {
		fmt.Fprintf(&text, "  marker block-%d { at = %dticks }\n", i+1, at)
		scene := scenes[entry.Scene]
		for _, setting := range scene.Settings {
			if setting.Path == "tempo" && setting.Number != nil {
				fmt.Fprintf(&text, "  tempo %s %s\n", FormatPosition(at), strconv.FormatFloat(math.Round(*setting.Number*1000)/1000, 'f', -1, 64))
			}
		}
		for _, track := range plan.Tracks {
			binding := scene.Bindings[track.ID]
			if binding == "" || binding == "keep" {
				continue
			}
			if part, ok := active[track.ID]; ok {
				flush(track.ID, part, at)
				delete(active, track.ID)
			}
			if binding != "off" {
				active[track.ID] = activePart{binding, at}
			}
		}
		at += int64(entry.Bars) * seq.TicksPerBar
	}
	for _, track := range plan.Tracks {
		if part, ok := active[track.ID]; ok {
			flush(track.ID, part, at)
		}
	}
	text.WriteString("}\n")
	return ReplaceSpan(source, file, Span{start, end, strings.ReplaceAll(text.String(), "\n", newline), file})
}
