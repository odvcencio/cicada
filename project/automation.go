package project

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/cmd"
	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/notation"
)

type AutomationLane struct {
	Path    string            `cicada:"Registered parameter path" json:"path" introduced:"cicada.project/2"`
	Points  []AutomationPoint `cicada:"Ordered musical control points" json:"points" introduced:"cicada.project/2"`
	Recalls []int64           `cicada:"Musical positions that reapply the curve without changing its shape" unit:"tick" json:"recalls,omitempty" introduced:"cicada.project/2"`
}

type AutomationPoint struct {
	Tick  int64      `cicada:"Musical position at 960 PPQ" unit:"tick" range:"0.." json:"tick" introduced:"cicada.project/2"`
	Value SceneValue `cicada:"Parameter value in base units" json:"value" introduced:"cicada.project/2"`
	Shape string     `cicada:"Incoming segment shape" json:"shape" introduced:"cicada.project/2"`
	Curve float64    `cicada:"Curve tension" range:"-8..8" json:"curve,omitempty" introduced:"cicada.project/2"`
}

func lowerAutomation(p *Project, score *notation.Score) []notation.Diagnostic {
	// Out-of-range points are all reported; the first point of any other kind
	// of error still ends the pass.
	var ranges []notation.Diagnostic
	type control struct {
		track uint8
		id    uint16
	}
	controls := map[control]string{} // resolved control -> first lane's path
	for _, source := range score.Automation {
		lane := AutomationLane{Path: source.Path, Points: []AutomationPoint{}}
		resolved, err := ResolveParameterPath(p, source.Path)
		if err != nil {
			code := "CICADA-REFERENCE"
			var pathErr *PathError
			if errors.As(err, &pathErr) && pathErr.Code != "" {
				code = pathErr.Code
			}
			return append(ranges, automationDiagnostic(code, err, source.Position)...)
		}
		// Two spellings of one control, such as a.send.hall and a.send.reverb,
		// would fight over it.
		key := control{resolved.Track, uint16(resolved.ID)}
		if first, ok := controls[key]; ok && first != source.Path {
			return append(ranges, automationDiagnostic("CICADA-DUPLICATE", fmt.Errorf("automation paths %s and %s set the same control%s", first, source.Path, sendPathNote(p, first, source.Path)), source.Position)...)
		}
		controls[key] = source.Path
		send := paramdefs.IsTrackSend(resolved.Descriptor)
		firstUnit := ""
		for i, point := range source.Points {
			tick, err := notation.PositionTick(p.MeterMap(), point.At)
			if err != nil {
				return append(ranges, automationDiagnostic("CICADA-POSITION", err, point.Position)...)
			}
			if message := paramdefs.RangeMessage(resolved.Descriptor, source.Path+" "+point.At, point.Value); message != "" {
				ranges = append(ranges, notation.Diagnostic{Code: "CICADA-UNIT", Message: message, Severity: "error", Position: point.Position})
				continue
			}
			var value Value
			if send {
				// A send lane's unit is the unit of its points: dB (or off,
				// silence) or a linear gain, the same for every point.
				value, err = sendPointValue(point.Value)
				if err == nil {
					unit := sendLaneUnit(value)
					if i == 0 {
						firstUnit = unit
					} else if unit != firstUnit {
						err = fmt.Errorf("automation %s mixes units: %s %s is %s and %s %s is %s; write every point of a send lane in dB (off for silence) or every point as a linear gain from 0 to 1", source.Path, source.Points[0].At, source.Points[0].Value, firstUnit, point.At, point.Value, unit)
					}
				}
			} else {
				value, err = parseParameterValue(resolved.Descriptor, point.Value)
			}
			if err != nil {
				return append(ranges, automationDiagnostic("CICADA-UNIT", err, point.Position)...)
			}
			shape := point.Shape
			if shape == "" {
				shape = "linear"
			}
			var curve float64
			if point.Curve != "" {
				curve, err = strconv.ParseFloat(point.Curve, 64)
				if err != nil || shape != "curve" {
					return append(ranges, automationDiagnostic("CICADA-PARAM", fmt.Errorf("only curve takes unitless tension"), point.Position)...)
				}
			} else if shape == "curve" {
				return append(ranges, automationDiagnostic("CICADA-PARAM", fmt.Errorf("curve needs tension -8..8"), point.Position)...)
			}
			lane.Points = append(lane.Points, AutomationPoint{Tick: tick, Value: sceneValueFrom(value), Shape: shape, Curve: curve})
		}
		for _, recall := range source.Recalls {
			tick, err := notation.PositionTick(p.MeterMap(), recall.At)
			if err != nil {
				return append(ranges, automationDiagnostic("CICADA-POSITION", err, recall.Position)...)
			}
			lane.Recalls = append(lane.Recalls, tick)
		}
		p.Automation = append(p.Automation, lane)
	}
	if len(ranges) > 0 {
		return ranges
	}
	if len(p.Automation) > 0 {
		p.Format, p.Version = FormatID2, 2
	}
	return nil
}

func automationDiagnostic(code string, err error, position notation.Position) []notation.Diagnostic {
	return []notation.Diagnostic{{Code: code, Message: err.Error(), Severity: "error", Position: position}}
}

func validateAutomation(p *Project) error {
	if len(p.Automation) == 0 {
		return nil
	}
	if p.Format != FormatID2 {
		return fmt.Errorf("CICADA-VERSION: automation requires cicada.project/2")
	}
	if len(p.Automation) > 32 {
		return fmt.Errorf("CICADA-LIMIT: at most 32 automation lanes")
	}
	var end int64
	if p.Arrange != nil {
		// Placements define playback duration; markers do not extend it.
		// Keep accepting a control point exactly at the exclusive end, as
		// scene songs do, so a ramp can reach its final value there.
		for _, placement := range p.Arrange.Placements {
			end = max(end, placement.AtTick+placement.LengthTicks)
		}
	} else {
		end = p.SongEndTick()
	}
	seen := map[string]bool{}
	type slot struct {
		track uint8
		id    uint16
	}
	slots := map[slot]string{}
	for _, lane := range p.Automation {
		if seen[lane.Path] {
			return fmt.Errorf("CICADA-DUPLICATE: automation path %s", lane.Path)
		}
		seen[lane.Path] = true
		resolved, err := ResolveParameterPath(p, lane.Path)
		if err != nil {
			return err
		}
		// Two spellings of one control, such as a.send.hall and
		// a.send.reverb, would fight over it.
		key := slot{resolved.Track, uint16(resolved.ID)}
		if other, ok := slots[key]; ok {
			return fmt.Errorf("CICADA-DUPLICATE: automation paths %s and %s set the same control%s", other, lane.Path, sendPathNote(p, other, lane.Path))
		}
		slots[key] = lane.Path
		if !resolved.Descriptor.Automatable || !resolved.Descriptor.Live || resolved.Descriptor.Unit == "enum" || resolved.Descriptor.Curve == "toggle" {
			return fmt.Errorf("CICADA-UNSUPPORTED: %s has no continuous numeric control", lane.Path)
		}
		if len(lane.Points) == 0 || len(lane.Points) > 1024 {
			return fmt.Errorf("CICADA-LIMIT: automation needs 1..1024 points")
		}
		if len(lane.Recalls) > 1024 {
			return fmt.Errorf("CICADA-LIMIT: automation supports at most 1024 recalls per lane")
		}
		seenRecalls := map[int64]bool{}
		for _, tick := range lane.Recalls {
			if tick < lane.Points[0].Tick || tick > end || tick%240 != 0 || seenRecalls[tick] {
				return fmt.Errorf("CICADA-POSITION: automation recalls must be unique sixteenths after the first point within the song")
			}
			seenRecalls[tick] = true
		}

		for i, point := range lane.Points {
			if point.Tick < 0 || point.Tick > end || point.Tick%240 != 0 || i > 0 && point.Tick <= lane.Points[i-1].Tick {
				return fmt.Errorf("CICADA-POSITION: automation points must increase on sixteenths within the song")
			}
			if paramdefs.IsTrackSend(resolved.Descriptor) && (point.Value.Unit == "db" || point.Value.Unit == "enum") {
				// A send lane written in dB, with off for silence.
				if _, err := sendPointValue(sceneValueSource(point.Value)); err != nil {
					return fmt.Errorf("CICADA-UNIT: %s point %d: %w", lane.Path, i+1, err)
				}
				if unit := sendLaneUnit(point.Value.projectValue()); unit != sendLaneUnit(lane.Points[0].Value.projectValue()) {
					return fmt.Errorf("CICADA-UNIT: automation %s mixes units: point 1 is %s and point %d is %s; write every point of a send lane in dB (off for silence) or every point as a linear gain from 0 to 1", lane.Path, sendLaneUnit(lane.Points[0].Value.projectValue()), i+1, unit)
				}
			} else {
				if point.Value.Number == nil || point.Value.Text != "" {
					return fmt.Errorf("CICADA-UNIT: continuous automation needs numeric values")
				}
				if message := paramdefs.RangeMessageValue(resolved.Descriptor, fmt.Sprintf("%s point %d", lane.Path, i+1), *point.Value.Number); message != "" {
					return fmt.Errorf("CICADA-UNIT: %s", message)
				}
				if err := validateParameterValue(resolved.Descriptor, point.Value.projectValue()); err != nil {
					return fmt.Errorf("CICADA-UNIT: %w", err)
				}
				if paramdefs.IsTrackSend(resolved.Descriptor) && sendLaneUnit(lane.Points[0].Value.projectValue()) != "a linear gain" {
					return fmt.Errorf("CICADA-UNIT: automation %s mixes units: point 1 is %s and point %d is a linear gain; write every point of a send lane in dB (off for silence) or every point as a linear gain from 0 to 1", lane.Path, sendLaneUnit(lane.Points[0].Value.projectValue()), i+1)
				}
			}
			if point.Shape != "linear" && point.Shape != "exponential" && point.Shape != "step" && point.Shape != "smooth" && point.Shape != "curve" {
				return fmt.Errorf("CICADA-UNSUPPORTED: automation shape %s", point.Shape)
			}
			if !finite(point.Curve) || math.Abs(point.Curve) > 8 || point.Shape != "curve" && point.Curve != 0 {
				return fmt.Errorf("CICADA-PARAM: curve tension must be -8..8")
			}
			if resolved.Descriptor.Curve == "log" && point.Value.Number != nil && *point.Value.Number <= 0 {
				return fmt.Errorf("CICADA-PARAM: logarithmic automation needs positive values")
			}
		}
	}
	return nil
}

// AutomationValue evaluates the same control-space curve used for playback and
// the Studio display. Before the first point a lane contributes no value.
func AutomationValue(p *Project, lane AutomationLane, tick int64) (float64, bool) {
	space := linearSpace
	if len(lane.Points) > 0 && tick >= lane.Points[0].Tick {
		if resolved, err := ResolveParameterPath(p, lane.Path); err == nil {
			space = automationSpaceOf(resolved.Descriptor, lane)
		}
	}
	return automationValue(lane, tick, space)
}

// automationSpace is where a lane interpolates: its parameter's base unit, its
// logarithm (frequency and time parameters), or, for a send lane written in
// dB, its level in dB.
type automationSpace uint8

const (
	linearSpace automationSpace = iota
	logSpace
	decibelSpace
)

func automationSpaceOf(descriptor paramdefs.Descriptor, lane AutomationLane) automationSpace {
	switch {
	case paramdefs.IsTrackSend(descriptor) && decibelPoints(lane):
		return decibelSpace
	case descriptor.Curve == "log":
		return logSpace
	}
	return linearSpace
}

// Grid compilation already resolved the lane; reuse its control space rather
// than looking up the same registry metadata for every sample and emission.
func automationValue(lane AutomationLane, tick int64, space automationSpace) (float64, bool) {
	if len(lane.Points) == 0 || tick < lane.Points[0].Tick {
		return 0, false
	}
	i := sort.Search(len(lane.Points), func(i int) bool { return lane.Points[i].Tick > tick })
	a := lane.Points[i-1]
	decibel, logarithmic := space == decibelSpace, space == logSpace
	if i == len(lane.Points) {
		if decibel {
			return sendPointGain(a.Value), true
		}
		return *a.Value.Number, true
	}
	b := lane.Points[i]
	t := float64(tick-a.Tick) / float64(b.Tick-a.Tick)
	switch b.Shape {
	case "step":
		t = 0
	case "smooth":
		t = t * t * (3 - 2*t)
	case "curve":
		t = math.Pow(t, math.Exp2(b.Curve))
	}
	if decibel {
		// A send lane written in dB ramps linearly in dB, as level does. A
		// segment that starts or ends in silence (off) ramps linearly in gain,
		// since silence has no finite level.
		if a.Value.Unit == "enum" || b.Value.Unit == "enum" {
			x, y := sendPointGain(a.Value), sendPointGain(b.Value)
			return x + float64((y-x)*t), true
		}
		x, y := *a.Value.Number, *b.Value.Number
		return paramdefs.SendGainFromDB(x + float64((y-x)*t)), true
	}
	x, y := *a.Value.Number, *b.Value.Number
	if logarithmic {
		return math.Exp(float64(math.Log(x)) + float64((math.Log(y)-math.Log(x))*t)), true
	}
	return x + float64((y-x)*t), true
}

// decibelPoints reports whether a send lane holds dB points, with off for
// silence, rather than linear gains.
func decibelPoints(lane AutomationLane) bool {
	return len(lane.Points) > 0 && (lane.Points[0].Value.Unit == "db" || lane.Points[0].Value.Unit == "enum" && lane.Points[0].Value.Text == "off")
}

// sendPointValue reads one point of a send lane: a level from -60 dB to 0 dB,
// off for silence, or a linear gain from 0 to 1. A dB point keeps its level;
// the lane converts it to gain as it plays.
func sendPointValue(source string) (Value, error) {
	if source == "off" {
		return Value{Unit: "enum", Text: "off"}, nil
	}
	value, err := projectValue(source)
	if err != nil {
		return Value{}, err
	}
	if value.Unit == "db" && value.Number != nil {
		if db := *value.Number; math.IsNaN(db) || db < paramdefs.SendMinDB || db > paramdefs.SendMaxDB {
			return Value{}, fmt.Errorf("send level %s is outside %ddB to %ddB; write off for silence", source, paramdefs.SendMinDB, paramdefs.SendMaxDB)
		}
		return value, nil
	}
	descriptor, _ := paramdefs.Lookup("mix.send_b")
	return parseParameterValue(descriptor, source)
}

// sendLaneUnit names the unit class of a send point, for the rule that one
// lane keeps one unit.
func sendLaneUnit(value Value) string {
	if value.Unit == "db" || value.Unit == "enum" && value.Text == "off" {
		return "in dB"
	}
	return "a linear gain"
}

// sendPointGain is the linear gain of a dB send point; off is silence.
func sendPointGain(value SceneValue) float64 {
	if value.Unit != "db" || value.Number == nil {
		return 0
	}
	return paramdefs.SendGainFromDB(*value.Number)
}

// sceneValueSource writes a stored point back as the source text
// sendPointValue reads.
func sceneValueSource(value SceneValue) string {
	if value.Unit == "enum" {
		return value.Text
	}
	text, err := valueSource(value.projectValue())
	if err != nil {
		return ""
	}
	return text
}

const (
	// automationGridTicks is the finest control spacing: four ticks, at most
	// 2.1 ms at 120 BPM.
	automationGridTicks = 4
	// automationResolution divides a parameter's range (its log range for log
	// curves) into the smallest change that earns a new control.
	automationResolution = 4096
	// automationMaxGapTicks bounds the time between controls of a moving
	// segment (a sixteenth note at 960 PPQ is 240 ticks).
	automationMaxGapTicks = 240
	// automationMaxControls is the limit of the kernel image control count,
	// which is stored as a 16-bit value.
	automationMaxControls = 65535
)

// CompileAutomation prepares controls on a four-tick grid. A moving segment
// emits a control only when the value has moved by one resolution step since
// the last control, or after a bounded gap, and always at its endpoints, so a
// fast segment keeps four-tick spacing and a long slow one uses far fewer
// controls. Existing parameter smoothing connects those targets continuously.
func CompileAutomation(p *Project) ([]cmd.Command, error) {
	var controls []cmd.Command
	for _, lane := range p.Automation {
		resolved, err := ResolveParameterPath(p, lane.Path)
		if err != nil {
			return nil, err
		}
		last := uint32(0)
		measure := func(v float64) float64 { return v }
		step := (resolved.Descriptor.Max - resolved.Descriptor.Min) / automationResolution
		if resolved.Descriptor.Curve == "log" && resolved.Descriptor.Min > 0 {
			measure = math.Log
			step = (math.Log(resolved.Descriptor.Max) - math.Log(resolved.Descriptor.Min)) / automationResolution
		}
		space := automationSpaceOf(resolved.Descriptor, lane)
		if space == decibelSpace {
			// A dB lane earns a new control for each step of its 60 dB range,
			// measured in dB, so quiet passages move as finely as loud ones.
			measure = func(v float64) float64 { return 20 * detmath.Log10(math.Max(v, 1e-6)) }
			step = float64(paramdefs.SendMaxDB-paramdefs.SendMinDB) / automationResolution
		}
		var lastMeasure float64
		var lastTick int64
		emit := func(tick int64) error {
			value, _ := automationValue(lane, tick, space)
			compiled, err := ParameterFloat32Value(resolved.Descriptor.Min, resolved.Descriptor.Max, value)
			if err != nil {
				return err
			}
			bits := math.Float32bits(compiled)
			if tick != lane.Points[0].Tick && bits == last {
				return nil
			}
			controls = append(controls, cmd.Command{Op: cmd.OpSetParam, Track: resolved.Track, Index: uint16(resolved.ID), Arg0: bits, Tick: tick})
			if len(controls) > automationMaxControls {
				return fmt.Errorf("CICADA-LIMIT: automation exceeds %d prepared controls", automationMaxControls)
			}
			last, lastMeasure, lastTick = bits, measure(float64(compiled)), tick
			return nil
		}
		if err := emit(lane.Points[0].Tick); err != nil {
			return nil, err
		}
		for i := 1; i < len(lane.Points); i++ {
			a, b := lane.Points[i-1], lane.Points[i]
			if b.Shape == "step" || sameAutomationValue(a.Value, b.Value) {
				if err := emit(b.Tick); err != nil {
					return nil, err
				}
				continue
			}
			for tick := a.Tick + automationGridTicks; tick < b.Tick; tick += automationGridTicks {
				value, _ := automationValue(lane, tick, space)
				if math.Abs(measure(value)-lastMeasure) < step && tick-lastTick < automationMaxGapTicks {
					continue
				}
				if err := emit(tick); err != nil {
					return nil, err
				}
			}
			if err := emit(b.Tick); err != nil {
				return nil, err
			}
		}

		// Reapply held values after every authored scene switch, including
		// sparse step lanes and points that end before the song does.
		var boundary, bar int64
		for i, entry := range p.Song {
			if i > 0 && boundary >= lane.Points[0].Tick {
				last = math.Float32bits(float32(math.NaN()))
				if err := emit(boundary); err != nil {
					return nil, err
				}
			}
			bar += int64(entry.Bars)
			boundary = p.MeterMap().BarStart(bar)
		}
		for _, tick := range lane.Recalls {
			last = math.Float32bits(float32(math.NaN()))
			if err := emit(tick); err != nil {
				return nil, err
			}
		}

	}
	sort.SliceStable(controls, func(i, j int) bool { return controls[i].Tick < controls[j].Tick })
	return controls, nil
}

// sendPathNote explains, for a diagnostic about paths a and b, a send path
// whose declared effect name is the other return's keyword.
func sendPathNote(p *Project, a, b string) string {
	for _, path := range []string{a, b} {
		if _, setting, ok := strings.Cut(path, "."); ok {
			if note := paramdefs.SendTargetNote(setting, projectEffectKind(p)); note != "" {
				return "; " + note
			}
		}
	}
	return ""
}

// sameAutomationValue reports whether two points hold the same value, so the
// segment between them is flat.
func sameAutomationValue(a, b SceneValue) bool {
	if a.Number == nil || b.Number == nil {
		return a.Number == nil && b.Number == nil && a.Unit == b.Unit && a.Text == b.Text
	}
	return a.Unit == b.Unit && *a.Number == *b.Number
}

// AutomationPointValue is a point's value in its parameter's base unit: a dB
// send point's linear gain, 0 for off, and any other point's number.
func AutomationPointValue(point AutomationPoint) float64 {
	if point.Value.Unit == "db" || point.Value.Unit == "enum" {
		return sendPointGain(point.Value)
	}
	if point.Value.Number == nil {
		return 0
	}
	return *point.Value.Number
}

// AutomationPointText is a point's value as the score writes it, such as
// -6dB, off or 0.5.
func AutomationPointText(point AutomationPoint) string {
	return sceneValueSource(point.Value)
}
