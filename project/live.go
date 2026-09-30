package project

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/notation"
)

// Live is the edition-2 host control contract. A nil Live preserves the
// interchange representation and command stream of existing scores.
type Live struct {
	Land       string       `cicada:"Default launch landing" json:"land,omitempty" introduced:"cicada.project/2"`
	PhraseBars int          `cicada:"Phrase length in bars" json:"phrase_bars,omitempty" introduced:"cicada.project/2"`
	Macros     []LiveMacro  `cicada:"Macros in host ID order" json:"macros" introduced:"cicada.project/2"`
	Layers     []LiveLayers `cicada:"Macro-controlled track layers" json:"layers" introduced:"cicada.project/2"`
}

type LiveMacro struct {
	Name     string  `cicada:"Host control name" json:"name" introduced:"cicada.project/2"`
	Default  float64 `cicada:"Initial macro value" json:"default" introduced:"cicada.project/2"`
	SmoothMS float64 `cicada:"Default smoothing duration in milliseconds" json:"smooth_ms" introduced:"cicada.project/2"`
}

type LiveLayers struct {
	Macro       string      `cicada:"Controlling macro name" json:"macro" introduced:"cicada.project/2"`
	Rules       []LiveLayer `cicada:"Track activation thresholds" json:"rules" introduced:"cicada.project/2"`
	AttackBars  int         `cicada:"Bars per rising level" json:"attack_bars" introduced:"cicada.project/2"`
	ReleaseBars int         `cicada:"Quiet bars per falling level" json:"release_bars" introduced:"cicada.project/2"`
}

type LiveLayer struct {
	Track     string  `cicada:"Controlled track identifier" json:"track" introduced:"cicada.project/2"`
	Threshold float64 `cicada:"Activation threshold" json:"threshold" introduced:"cicada.project/2"`
}

func liveFromScore(source *notation.Live) *Live {
	if source == nil {
		return nil
	}
	live := &Live{Land: source.Land, PhraseBars: source.PhraseBars, Macros: []LiveMacro{}, Layers: []LiveLayers{}}
	for _, m := range source.Macros {
		live.Macros = append(live.Macros, LiveMacro{Name: m.Name, Default: m.Value, SmoothMS: m.SmoothMS})
	}
	for _, l := range source.Layers {
		layers := LiveLayers{Macro: l.Macro, AttackBars: l.AttackBars, ReleaseBars: l.ReleaseBars, Rules: []LiveLayer{}}
		for _, r := range l.Rules {
			layers.Rules = append(layers.Rules, LiveLayer{Track: r.Track, Threshold: r.Value})
		}
		live.Layers = append(live.Layers, layers)
	}
	return live
}

func liveToScore(source *Live) *notation.Live {
	if source == nil {
		return nil
	}
	live := &notation.Live{Land: source.Land, PhraseBars: source.PhraseBars}
	for _, m := range source.Macros {
		live.Macros = append(live.Macros, notation.LiveMacro{Name: m.Name, Value: m.Default, SmoothMS: m.SmoothMS})
	}
	for _, l := range source.Layers {
		layers := notation.LiveLayers{Macro: l.Macro, AttackBars: l.AttackBars, ReleaseBars: l.ReleaseBars}
		for _, r := range l.Rules {
			layers.Rules = append(layers.Rules, notation.LiveLayer{Track: r.Track, Value: r.Threshold})
		}
		live.Layers = append(live.Layers, layers)
	}
	return live
}

// LiveCommands lowers a validated project's live declarations to kernel setup
// commands. It defines initial values without starting smoothing ramps.
// Thresholds use the kernel's rounded 8-bit values; equal packed values share
// a level and packed zero joins the base mask (zero is the unused sentinel).
func LiveCommands(p *Project) []cmd.Command {
	if p == nil || p.Live == nil {
		return nil
	}
	var commands []cmd.Command
	ids := map[string]uint16{}
	for i, macro := range p.Live.Macros {
		ids[macro.Name] = uint16(i)
		commands = append(commands, cmd.Command{Op: cmd.OpDefineMacro, Track: 255, Index: uint16(i), Arg0: math.Float32bits(float32(macro.Default))})
	}
	for _, layers := range p.Live.Layers {
		byTrack := map[string]uint8{}
		distinct := map[uint8]bool{}
		var thresholds []int
		for _, rule := range layers.Rules {
			threshold := uint8(math.Round(rule.Threshold * 255))
			byTrack[rule.Track] = threshold
			if threshold != 0 && !distinct[threshold] {
				thresholds = append(thresholds, int(threshold))
				distinct[threshold] = true
			}
		}
		sort.Ints(thresholds)
		var packed uint32
		for i, threshold := range thresholds {
			packed |= uint32(threshold) << (8 * i)
		}
		var masks [4]uint16
		for i, track := range p.Tracks {
			threshold, hasRule := byTrack[track.ID]
			for level := range 4 {
				if !hasRule || threshold == 0 || level > 0 && len(thresholds) > 0 && int(threshold) <= thresholds[min(level, len(thresholds))-1] {
					masks[level] |= 1 << i
				}
			}
		}
		id := ids[layers.Macro]
		commands = append(commands,
			cmd.Command{Op: cmd.OpSetLayers, Track: 255, Index: id, Arg0: packed, Arg1: uint32(layers.ReleaseBars)},
			cmd.Command{Op: cmd.OpSetLayerMasks, Track: 255, Index: id, Arg0: uint32(masks[0]) | uint32(masks[1])<<16, Arg1: uint32(masks[2]) | uint32(masks[3])<<16})
	}
	if p.Live.PhraseBars != 0 {
		commands = append(commands, cmd.Command{Op: cmd.OpSetPhraseBars, Track: 255, Arg0: uint32(p.Live.PhraseBars)})
	}
	return commands
}

// LiveSurface is the ordered name-to-ID table for a host. Macro slice indexes
// are kernel IDs. Land is stored for future launch scheduling.
type LiveSurface struct {
	Land       string
	PhraseBars int
	Macros     []LiveMacro
}

// LiveSurfaceOf returns an independent copy of the host control defaults.
func LiveSurfaceOf(p *Project) LiveSurface {
	if p == nil || p.Live == nil {
		return LiveSurface{}
	}
	return LiveSurface{Land: p.Live.Land, PhraseBars: p.Live.PhraseBars, Macros: append([]LiveMacro{}, p.Live.Macros...)}
}

// SmoothingFrames converts the default duration at the host's sample rate,
// rounded to the nearest frame and capped to the kernel's uint32 payload.
func (m LiveMacro) SmoothingFrames(sampleRate int) uint32 {
	if sampleRate <= 0 || m.SmoothMS <= 0 || math.IsNaN(m.SmoothMS) {
		return 0
	}
	frames := math.Round(m.SmoothMS * float64(sampleRate) / 1000)
	if frames >= math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(frames)
}

func liveSource(live *Live) string {
	var out strings.Builder
	out.WriteString("live {\n")
	if live.Land != "" {
		out.WriteString("  land = " + live.Land + "\n")
	}
	if live.PhraseBars != 0 {
		out.WriteString("  phrase = " + strconv.Itoa(live.PhraseBars) + "bars\n")
	}
	for _, m := range live.Macros {
		out.WriteString("  macro " + m.Name + " = " + decimal(m.Default))
		if m.SmoothMS > 0 {
			out.WriteString(" smooth " + decimal(m.SmoothMS) + "ms")
		}
		out.WriteByte('\n')
	}
	for _, l := range live.Layers {
		out.WriteString("  layers " + l.Macro + " {\n")
		for _, r := range l.Rules {
			out.WriteString("    " + r.Track + " >= " + decimal(r.Threshold) + "\n")
		}
		out.WriteString("    attack 1bar\n    release " + strconv.Itoa(l.ReleaseBars) + "bars\n  }\n")
	}
	out.WriteByte('}')
	return out.String()
}
