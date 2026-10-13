package edit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/seq"
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
	Track    string         `json:"track"`
	Tempo    int64          `json:"tempoMilli"`
	Segments []VocalSegment `json:"segments"`
	Asset    string         `json:"asset"`
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

func vocalCompSource(ctx *Context, in PlaceVocalComp) ([]byte, error) {
	next, track := in.Comp, in.Comp.Track
	if len(next.Segments) == 0 {
		return nil, fmt.Errorf("choose a vocal phrase before printing")
	}
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, err
	}
	source, file := ctx.Source, ctx.Options.Path
	source, err = vocalArrangementSource(source, file, plan)
	if err != nil {
		return nil, err
	}
	// Remove just the managed block and its placement, preserving every other byte.
	if in.Previous != nil {
		previous := VocalComp{Track: track, Asset: in.Previous.Asset}
		begin := []byte("// cicada-vocal-begin " + track + "\n")
		finish := []byte("// cicada-vocal-end " + track + "\n")
		a := bytes.Index(source, begin)
		if a < 0 {
			return nil, fmt.Errorf("vocal comp declarations were edited; restore them before selecting")
		}
		b := bytes.Index(source[a:], finish)
		if b < 0 {
			return nil, fmt.Errorf("vocal comp declarations are incomplete")
		}
		// Generic score edits can append an effect or another declaration before
		// the trailing managed comment. Delete only our print declarations and
		// metadata, retaining those user/application edits byte for byte.
		block := source[a : a+b+len(finish)]
		kept, e := removeVocalPrintDeclarations(block, file, previous, begin, finish)
		if e != nil {
			return nil, e
		}
		source, err = ReplaceSpan(source, file, Span{a, a + b + len(finish), string(kept), file})
		if err != nil {
			return nil, err
		}
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var spans []Span
	foundArrangement := false
	placeID := "vocal-" + track
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) != "arrange_decl" {
			continue
		}
		foundArrangement = true
		for j := 0; j < n.NamedChildCount(); j++ {
			child := n.NamedChild(j)
			if w.Type(child) == "place_decl" && w.Text(w.Field(child, "name")) == placeID {
				if in.Previous == nil {
					return nil, fmt.Errorf("placement name %s already exists", placeID)
				}
				spans = append(spans, Span{int(child.StartByte()), int(child.EndByte()), "", file})
			}
		}
		first, last := next.Segments[0].Start, next.Segments[len(next.Segments)-1].End
		line := fmt.Sprintf("\n  place %s %s %s-clip { at = %dticks length = %dticks }\n", placeID, track, next.Asset, first, last-first)
		spans = append(spans, Span{int(n.EndByte()) - 1, int(n.EndByte()) - 1, line, file})
	}
	if !foundArrangement {
		return nil, fmt.Errorf("open the score file containing the arrangement before selecting a vocal")
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start > spans[j].Start })
	source, err = PatchSpans(source, file, spans)
	if err != nil {
		return nil, err
	}
	asset := in.Asset
	metadata, _ := json.Marshal(next)
	text := fmt.Sprintf("\n// cicada-vocal-begin %s\n%s%s\nasset %s %s { sha256 = %s format = wav frames = %d rate = %dHz channels = 1 source = recorded }\nclip %s-clip %s { start = 0frames end = %dframes }\n// cicada-vocal-end %s\n", track, VocalCompPrefix, metadata, next.Asset, strconv.Quote(asset.Path), strconv.Quote(asset.SHA256), asset.Frames, asset.RateHz, next.Asset, next.Asset, asset.Frames, track)
	return append(source, []byte(text)...), nil
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
func vocalArrangementSource(source []byte, file string, plan *Plan) ([]byte, error) {
	if plan.HasArrange {
		return source, nil
	}
	root, w, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var start, end int
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if w.Type(n) == "song_decl" {
			start, end = int(n.StartByte()), int(n.EndByte())
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
	return ReplaceSpan(source, file, Span{start, end, text.String(), file})
}
