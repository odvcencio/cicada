package project

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/cmd"
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
	for _, source := range score.Automation {
		lane := AutomationLane{Path: source.Path, Points: []AutomationPoint{}}
		resolved, err := ResolveParameterPath(p, source.Path)
		if err != nil {
			return append(ranges, automationDiagnostic("CICADA-REFERENCE", err, source.Position)...)
		}
		for _, point := range source.Points {
			tick, err := notation.PositionTick(point.At)
			if err != nil {
				return append(ranges, automationDiagnostic("CICADA-POSITION", err, point.Position)...)
			}
			if message := paramdefs.RangeMessage(resolved.Descriptor, source.Path+" "+point.At, point.Value); message != "" {
				ranges = append(ranges, notation.Diagnostic{Code: "CICADA-UNIT", Message: message, Severity: "error", Position: point.Position})
				continue
			}
			value, err := parseParameterValue(resolved.Descriptor, point.Value)
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
			tick, err := notation.PositionTick(recall.At)
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
		for _, entry := range p.Song {
			end += int64(entry.Bars) * 3840
		}
	}
	seen := map[string]bool{}
	for _, lane := range p.Automation {
		if seen[lane.Path] {
			return fmt.Errorf("CICADA-DUPLICATE: automation path %s", lane.Path)
		}
		seen[lane.Path] = true
		resolved, err := ResolveParameterPath(p, lane.Path)
		if err != nil {
			return err
		}
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
			if point.Value.Number == nil || point.Value.Text != "" {
				return fmt.Errorf("CICADA-UNIT: continuous automation needs numeric values")
			}
			if message := paramdefs.RangeMessageValue(resolved.Descriptor, fmt.Sprintf("%s point %d", lane.Path, i+1), *point.Value.Number); message != "" {
				return fmt.Errorf("CICADA-UNIT: %s", message)
			}
			if err := validateParameterValue(resolved.Descriptor, point.Value.projectValue()); err != nil {
				return fmt.Errorf("CICADA-UNIT: %w", err)
			}
			if point.Shape != "linear" && point.Shape != "exponential" && point.Shape != "step" && point.Shape != "smooth" && point.Shape != "curve" {
				return fmt.Errorf("CICADA-UNSUPPORTED: automation shape %s", point.Shape)
			}
			if !finite(point.Curve) || math.Abs(point.Curve) > 8 || point.Shape != "curve" && point.Curve != 0 {
				return fmt.Errorf("CICADA-PARAM: curve tension must be -8..8")
			}
			if resolved.Descriptor.Curve == "log" && *point.Value.Number <= 0 {
				return fmt.Errorf("CICADA-PARAM: logarithmic automation needs positive values")
			}
		}
	}
	return nil
}

// AutomationValue evaluates the same control-space curve used for playback and
// the Studio display. Before the first point a lane contributes no value.
func AutomationValue(p *Project, lane AutomationLane, tick int64) (float64, bool) {
	logarithmic := false
	if len(lane.Points) > 1 && tick >= lane.Points[0].Tick && tick < lane.Points[len(lane.Points)-1].Tick {
		resolved, err := ResolveParameterPath(p, lane.Path)
		logarithmic = err == nil && resolved.Descriptor.Curve == "log"
	}
	return automationValue(lane, tick, logarithmic)
}

// Grid compilation already resolved the lane; reuse its control space rather
// than looking up the same registry metadata for every sample and emission.
func automationValue(lane AutomationLane, tick int64, logarithmic bool) (float64, bool) {
	if len(lane.Points) == 0 || tick < lane.Points[0].Tick {
		return 0, false
	}
	i := sort.Search(len(lane.Points), func(i int) bool { return lane.Points[i].Tick > tick })
	a := lane.Points[i-1]
	if i == len(lane.Points) {
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
	x, y := *a.Value.Number, *b.Value.Number
	if logarithmic {
		return math.Exp(math.Log(x) + (math.Log(y)-math.Log(x))*t), true
	}
	return x + (y-x)*t, true
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
		logarithmic := resolved.Descriptor.Curve == "log"
		measure := func(v float64) float64 { return v }
		step := (resolved.Descriptor.Max - resolved.Descriptor.Min) / automationResolution
		if resolved.Descriptor.Curve == "log" && resolved.Descriptor.Min > 0 {
			measure = math.Log
			step = (math.Log(resolved.Descriptor.Max) - math.Log(resolved.Descriptor.Min)) / automationResolution
		}
		var lastMeasure float64
		var lastTick int64
		emit := func(tick int64) error {
			value, _ := automationValue(lane, tick, logarithmic)
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
			if b.Shape == "step" || *a.Value.Number == *b.Value.Number {
				if err := emit(b.Tick); err != nil {
					return nil, err
				}
				continue
			}
			for tick := a.Tick + automationGridTicks; tick < b.Tick; tick += automationGridTicks {
				value, _ := automationValue(lane, tick, logarithmic)
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
		var boundary int64
		for i, entry := range p.Song {
			if i > 0 && boundary >= lane.Points[0].Tick {
				last = math.Float32bits(float32(math.NaN()))
				if err := emit(boundary); err != nil {
					return nil, err
				}
			}
			boundary += int64(entry.Bars) * 3840
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
