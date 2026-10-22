package edit

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

type songControlPoint struct {
	tick    int64
	literal string
	bits    uint32
}

// carrySongAutomation turns the used scenes' numeric parameter changes into
// ordinary step lanes. An existing curve takes over at its first point, just
// as it does during scene playback; its incoming segment was previously unused.
func carrySongAutomation(ctx *Context, score *notation.Score, plan *Plan, songFile string) error {
	scenes := map[string]Scene{}
	for _, scene := range plan.Scenes {
		scenes[scene.ID] = scene
	}
	var paths []string
	points := map[string][]songControlPoint{}
	var tick int64
	for _, entry := range plan.Song {
		for _, setting := range scenes[entry.Scene].Settings {
			refuse := func() error {
				return fmt.Errorf("scene %s setting %s cannot be carried into numeric arrangement automation", entry.Scene, setting.Path)
			}
			if plan.ResolveParam == nil || setting.Number == nil || setting.Text != "" {
				return refuse()
			}
			resolved, err := plan.ResolveParam(setting.Path)
			if err != nil {
				return err
			}
			d := resolved.Descriptor
			if !d.Live || !d.Automatable || d.Unit == "enum" || d.Curve == "toggle" {
				return refuse()
			}
			d.DisplayStep = 0
			raw, err := json.Marshal(*setting.Number)
			if err != nil {
				return err
			}
			literal, err := CanonicalMixerLiteral(d, raw)
			if err != nil {
				return err
			}
			if _, found := points[setting.Path]; !found {
				paths = append(paths, setting.Path)
			}
			points[setting.Path] = append(points[setting.Path], songControlPoint{tick, literal, math.Float32bits(float32(*setting.Number))})
		}
		tick += int64(entry.Bars) * seq.TicksPerBar
	}

	// Every existing curve reapplies its value at song boundaries, even if no
	// scene names that parameter. Keep those events as explicit curve recalls.
	existingByPath := map[string]*notation.Automation{}
	for i := range score.Automation {
		lane := &score.Automation[i]
		existingByPath[lane.Path] = lane
		if _, found := points[lane.Path]; !found {
			paths = append(paths, lane.Path)
		}
	}
	for _, path := range paths {
		existing := existingByPath[path]
		owner := songFile
		carried := points[path]
		var recalls []int64
		seen := map[int64]bool{}
		addRecall := func(tick int64) {
			if !seen[tick] {
				seen[tick] = true
				recalls = append(recalls, tick)
			}
		}
		if existing != nil {
			firstTick, err := notation.PositionTick(existing.Points[0].At)
			if err != nil {
				return err
			}
			count := 0
			for count < len(carried) && carried[count].tick < firstTick {
				count++
			}
			carried = carried[:count]
			if len(carried)+len(existing.Points) > 1024 {
				return fmt.Errorf("carrying scene changes would exceed 1024 points for %s", path)
			}
			for _, recall := range existing.Recalls {
				at, err := notation.PositionTick(recall.At)
				if err != nil {
					return err
				}
				seen[at] = true
			}
			var boundary int64
			for i, entry := range plan.Song {
				if i > 0 && boundary >= firstTick {
					addRecall(boundary)
				}
				boundary += int64(entry.Bars) * seq.TicksPerBar
			}
			owner = existing.Position.File
			if owner == "" {
				owner = ctx.Options.Path
			}
		}
		before, ok := ctx.FileSource(owner)
		if !ok {
			return fmt.Errorf("automation %s is outside the editable project sources", path)
		}
		nl := Newline(before)
		var rows strings.Builder
		for i, point := range carried {
			if i > 0 && point.bits == carried[i-1].bits {
				addRecall(point.tick)
				continue
			}
			fmt.Fprintf(&rows, "%s %s step%s  ", FormatPosition(point.tick), point.literal, nl)
		}
		if existing != nil && rows.Len() == 0 && len(recalls) == 0 {
			continue
		}
		sort.Slice(recalls, func(i, j int) bool { return recalls[i] < recalls[j] })
		var recallRows strings.Builder
		for _, tick := range recalls {
			fmt.Fprintf(&recallRows, "  recall %s%s", FormatPosition(tick), nl)
		}
		var after []byte
		if existing == nil {
			block := nl + "automate " + path + " {" + nl + "  " + strings.TrimSuffix(rows.String(), "  ") + recallRows.String() + "}" + nl
			after = append(append([]byte(nil), before...), []byte(block)...)
		} else {
			var err error
			after, err = mergeSongControls(before, owner, path, rows.String(), recallRows.String())
			if err != nil {
				return err
			}
		}
		ctx.Source = stageProjectSource(ctx, owner, before, after)
	}

	return nil
}

func mergeSongControls(source []byte, file, path, rows, recalls string) ([]byte, error) {
	root, walker, err := notation.ParseTree(source)
	if err != nil {
		return nil, err
	}
	var first, target *gts.Node
	for i := 0; i < root.NamedChildCount(); i++ {
		lane := root.NamedChild(i)
		if walker.Type(lane) != "automate_decl" || walker.Text(walker.Field(lane, "path")) != path {
			continue
		}
		target = lane
		for j := 0; j < lane.NamedChildCount(); j++ {
			if node := lane.NamedChild(j); walker.Type(node) == "automation_point" {
				first = node
				break
			}
		}
		break
	}
	if first == nil {
		return nil, fmt.Errorf("automation %s no longer matches its source", path)
	}
	var spans []Span
	if recalls != "" {
		at := int(target.EndByte()) - 1
		spans = append(spans, Span{at, at, Newline(source) + recalls, file})
	}
	if rows != "" {
		spans = append(spans, Span{int(first.StartByte()), int(first.StartByte()), rows, file})
		shape := walker.Field(first, "shape")
		if shape != nil {
			spans = append(spans, Span{int(shape.StartByte()), int(shape.EndByte()), "step", file})
		} else {
			value := walker.Field(first, "value")
			spans = append(spans, Span{int(value.EndByte()), int(value.EndByte()), " step", file})
		}
		if curve := walker.Field(first, "curve"); curve != nil {
			spans = append(spans, Span{int(curve.StartByte()), int(curve.EndByte()), "", file})
		}
	}
	return PatchSpans(source, file, spans)
}
