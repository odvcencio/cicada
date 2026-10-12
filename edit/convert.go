package edit

import (
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// ConvertSongToArrange rewrites a `song` block as an `arrange` block of
// placements. It writes only when the render check shows the converted score
// renders to the same bytes; otherwise it refuses and names the cause.
type ConvertSongToArrange struct{}

func (ConvertSongToArrange) Kind() string { return "convertsongtoarrange" }

func init() {
	Register("convertsongtoarrange", func() Intent { return &ConvertSongToArrange{} })
	Handle("convertsongtoarrange", convertSongToArrange)
}

type convertedPlacement struct {
	name, track, content string
	at, length           int64
}

func refuseConvert(cause string) error {
	return &RefusalError{Intent: "ConvertSongToArrange", Cause: cause}
}

func convertSongToArrange(ctx *Context, _ Intent) error {
	score, ds, err := ctx.ParseProject()
	if err != nil {
		return err
	}
	if score == nil || hasErrors(ds) {
		return fmt.Errorf("score must validate before converting the song")
	}
	if len(score.Song) == 0 {
		return refuseConvert("the score has no song to convert")
	}
	for _, scene := range score.Scenes {
		if len(scene.Settings) > 0 {
			return refuseConvert("scene " + scene.Name + " has settings that arrange cannot carry yet")
		}
	}
	placements, err := songPlacements(score)
	if err != nil {
		return err
	}
	converted, err := convertedSource(ctx, score, placements)
	if err != nil {
		return err
	}
	check, err := renderCheckFor(ctx)
	if err != nil {
		return err
	}
	before, err := check.Hash(ctx.Source)
	if err != nil {
		return err
	}
	after, err := check.Hash(converted)
	if err != nil {
		return err
	}
	if before != after {
		return refuseConvert(convertCause(ctx, placements))
	}
	ctx.Source, ctx.plan = converted, nil
	ctx.SetLabel("Song converted to arrange")
	return nil
}

// songPlacements turns the song into placements. A binding starts a placement
// at its entry (even when it names the pattern already playing); `keep` and an
// omitted binding extend the track's open placement; `off` and `stop` close it.
func songPlacements(score *notation.Score) ([]convertedPlacement, error) {
	scenes := make(map[string]notation.Scene, len(score.Scenes))
	for _, scene := range score.Scenes {
		scenes[scene.Name] = scene
	}
	hasStopPattern := false
	for _, pattern := range score.Patterns {
		hasStopPattern = hasStopPattern || pattern.Name == "stop"
	}
	var placements []convertedPlacement
	open := make(map[string]int, len(score.Tracks))
	var tick int64
	for index, entry := range score.Song {
		bars := int64(entry.Bars)
		if bars < 1 {
			bars = 1
		}
		scene, ok := scenes[entry.Scene]
		if !ok {
			return nil, fmt.Errorf("song uses unknown scene %q", entry.Scene)
		}
		for _, track := range score.Tracks {
			action := "keep"
			for _, binding := range scene.Bindings {
				if binding.Track == track.Name {
					action = binding.Pattern
				}
			}
			switch {
			case action == "keep":
				if at, ok := open[track.Name]; ok {
					placements[at].length += bars * seq.TicksPerBar
				}
			case action == "off" || action == "stop" && !hasStopPattern:
				delete(open, track.Name)
			default:
				open[track.Name] = len(placements)
				placements = append(placements, convertedPlacement{
					name: fmt.Sprintf("%s-%d-%s", entry.Scene, index+1, track.Name), track: track.Name, content: action,
					at: tick, length: bars * seq.TicksPerBar,
				})
			}
		}
		tick += bars * seq.TicksPerBar
	}
	if len(placements) == 0 {
		return nil, refuseConvert("no scene binds a pattern, so the arrange would be empty")
	}
	return placements, nil
}

func convertedSource(ctx *Context, score *notation.Score, placements []convertedPlacement) ([]byte, error) {
	source := ctx.Source
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var song, header *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		switch walker.Type(node) {
		case "song_decl":
			if song == nil {
				song = node
			}
		case "integer":
			if header == nil {
				header = node
			}
		}
	}
	if song == nil {
		return nil, refuseConvert("the song is declared in another source file; convert it there")
	}
	nl := Newline(source)
	var block strings.Builder
	block.WriteString("arrange {")
	for _, p := range placements {
		fmt.Fprintf(&block, "%s  place %s %s %s {%s    at = %s length = %s%s  }", nl, p.name, p.track, p.content, nl, FormatPosition(p.at), FormatLength(p.length), nl)
	}
	block.WriteString(nl + "}")
	spans := []Span{{int(song.StartByte()), int(song.EndByte()), block.String(), ctx.Options.Path}}
	if score.Version != 2 {
		if ctx.Options.ManifestPath != "" {
			return nil, refuseConvert(fmt.Sprintf("the project manifest declares edition %d; arrange needs edition 2", score.Version))
		}
		if header != nil {
			spans = append(spans, Span{int(header.StartByte()), int(header.EndByte()), "2", ctx.Options.Path})
		} else {
			spans = append(spans, Span{0, 0, "cicada 2" + nl, ctx.Options.Path})
		}
	}
	return PatchSpans(source, ctx.Options.Path, spans)
}

// convertCause names why a conversion rendered differently when the plan
// shows a reason. The render check stays the ground truth: this only words the
// refusal.
func convertCause(ctx *Context, placements []convertedPlacement) string {
	plan, err := ctx.CurrentPlan()
	if err != nil || plan == nil {
		return "the converted score renders differently"
	}
	patterns := make(map[string]Pattern, len(plan.Patterns))
	for _, pattern := range plan.Patterns {
		patterns[pattern.ID] = pattern
	}
	var drums string
	for i, p := range placements {
		var next *convertedPlacement
		for j := i + 1; j < len(placements); j++ {
			if placements[j].track == p.track {
				next = &placements[j]
				break
			}
		}
		pattern, known := patterns[p.content]
		if next == nil || next.at != p.at+p.length || !known {
			continue
		}
		if unit := pattern.LengthTicks(); unit > 0 && p.length%unit != 0 {
			return fmt.Sprintf("pattern %s (%s) would restart out of phase at bar %d", pattern.ID, describeBars(unit), next.at/seq.TicksPerBar+1)
		}
		if pattern.Kind == "drums" && drums == "" {
			drums = fmt.Sprintf("drums track %s starts a new placement at bar %d, which renders differently from the song", p.track, next.at/seq.TicksPerBar+1)
		}
	}
	if drums != "" {
		return drums
	}
	return "the converted score renders differently"
}

func describeBars(ticks int64) string {
	switch {
	case ticks == seq.TicksPerBar:
		return "1 bar"
	case ticks%seq.TicksPerBar == 0:
		return fmt.Sprintf("%d bars", ticks/seq.TicksPerBar)
	}
	return fmt.Sprintf("%d ticks", ticks)
}
