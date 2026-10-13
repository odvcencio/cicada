package edit

import (
	"fmt"
	"math"
	"strconv"

	"m31labs.dev/cicada/kernel/limits"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

// PrintAsset is the audio file the host wrote for a sampler print. The file's
// bytes are the host's; the intent only declares the asset and its clip.
type PrintAsset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Frames int64  `json:"frames"`
	RateHz int    `json:"ratehz"`
}

// PlaceSamplerPrint declares a printed sampler performance as an asset and a
// clip, and places it on an audio track in the arrange block. The clip is
// named Name+"-clip", the asset Name+"-audio", and the placement Name. A track
// that does not exist yet is created as an audio track. The placement is as
// long as the print, rounded up to a tick, at the project tempo.
type PlaceSamplerPrint struct {
	Name  string     `json:"name"`
	Track string     `json:"track"`
	At    string     `json:"at"`
	Asset PrintAsset `json:"asset"`
}

func (PlaceSamplerPrint) Kind() string { return "placesamplerprint" }

func init() {
	Register("placesamplerprint", func() Intent { return &PlaceSamplerPrint{} })
	Handle("placesamplerprint", func(ctx *Context, intent Intent) error {
		updated, err := samplerPrintSource(ctx, *intent.(*PlaceSamplerPrint))
		if err != nil {
			return err
		}
		ctx.Source, ctx.plan = updated, nil
		ctx.SetLabel("Resample pads into Session")
		return nil
	})
}

func samplerPrintSource(ctx *Context, in PlaceSamplerPrint) ([]byte, error) {
	plan, err := ctx.CurrentPlan()
	if err != nil {
		return nil, err
	}
	if plan.Edition != 2 || !plan.HasArrange {
		return nil, fmt.Errorf("placing resamples requires an edition-2 score with a Session arrangement")
	}
	if !patternName.MatchString(in.Track) {
		return nil, fmt.Errorf("choose a lowercase audio track name")
	}
	found := false
	for _, t := range plan.Tracks {
		if t.ID == in.Track {
			if t.Kind != "audio" {
				return nil, fmt.Errorf("choose an audio track for the resample")
			}
			found = true
		}
	}
	source, file := ctx.Source, ctx.Options.Path
	if !found {
		if source, err = audioTrackSyntax(source, file, in.Track); err != nil {
			return nil, err
		}
	}
	assetName, clipName := in.Name+"-audio", in.Name+"-clip"
	a := in.Asset
	declarations := fmt.Sprintf("\nasset %s %s { sha256 = %s format = wav frames = %d rate = %dHz channels = 2 }\nclip %s %s { start = 0frames end = %dframes fade_in = 240frames fade_out = 240frames }\n", assetName, strconv.Quote(a.Path), strconv.Quote(a.SHA256), a.Frames, a.RateHz, clipName, assetName, a.Frames)
	// Add declarations without rewriting the musician's existing score.
	source = append(append([]byte(nil), source...), []byte(declarations)...)
	if a.RateHz <= 0 {
		return nil, fmt.Errorf("sampler print needs a sample rate")
	}
	length := int64(math.Ceil(float64(a.Frames) * float64(plan.TempoMilli) * float64(seq.PPQ) / (float64(a.RateHz) * 60000)))
	return arrangementSource(source, file, arrangementOp{action: "append", newName: in.Name, settings: &PlacementSettings{At: in.At, Length: fmt.Sprintf("%dticks", length), Track: in.Track, Content: clipName}})
}

// audioTrackSyntax adds an empty audio track before the first scene. Unlike
// AddAudioTrack it reads declarations from syntax alone: imported instruments
// are resolved by the enclosing project compile, so a score with unresolved
// imports can still gain a track. It ports newAudioTrackSource.
func audioTrackSyntax(source []byte, file, name string) ([]byte, error) {
	if !patternName.MatchString(name) {
		return nil, fmt.Errorf("choose a lowercase audio track name in an edition-2 score")
	}
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	if root.HasError() {
		return nil, fmt.Errorf("fix score syntax before adding a track")
	}
	tracks := 0
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) != "track_decl" {
			continue
		}
		tracks++
		if walker.Text(walker.Field(node, "name")) == name {
			return nil, fmt.Errorf("track %s already exists", name)
		}
	}
	if tracks >= limits.MaxTracks {
		return nil, fmt.Errorf("a project supports at most %d tracks", limits.MaxTracks)
	}
	at := len(source)
	for i := 0; i < root.NamedChildCount(); i++ {
		node := root.NamedChild(i)
		if walker.Type(node) == "scene_decl" {
			at = int(node.StartByte())
			break
		}
	}
	newline := Newline(source)
	text := "track " + name + " audio {}" + newline + newline
	if at > 0 && source[at-1] != '\n' {
		text = newline + text
	}
	return ReplaceSpan(source, file, Span{at, at, text, file})
}
