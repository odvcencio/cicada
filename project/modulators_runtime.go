package project

import (
	"fmt"
	"math"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/dsp/detmath"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/notation"
)

// compileModulation lowers the semantic modulators and routes to the engine's
// fixed-point tables. All floating-point work happens here, on the host, and the
// tables hold integers (and the route depth as a float32 in the row's units), so
// every target decodes the same values.
func compileModulation(p *Project, cfg *engine.Config) error {
	if len(p.Modulators) == 0 && len(p.Routes) == 0 {
		return nil
	}
	trackIndex := map[string]int{}
	for i, t := range p.Tracks {
		trackIndex[t.ID] = i
	}
	index := map[string]int{}
	waves := map[string]uint8{"sine": engine.ModWaveSine, "triangle": engine.ModWaveTriangle, "square": engine.ModWaveSquare, "sample-hold": engine.ModWaveSampleHold}
	shapes := map[string]uint8{"linear": engine.ModShapeLinear, "step": engine.ModShapeStep, "smooth": engine.ModShapeSmooth, "exponential": engine.ModShapeExponential}
	for i, m := range p.Modulators {
		index[m.ID] = i
		spec := engine.ModulatorSpec{RateMicroHz: uint64(m.RateMicroHz), Continue: m.Loop == "continue"}
		if m.RateTicks > 0 {
			spec.RateTicks = uint32(m.RateTicks)
		}
		switch m.Kind {
		case notation.ModLFO:
			spec.Kind, spec.Wave = engine.ModKindLFO, waves[m.Wave]
			spec.PhaseQ32 = uint32(math.Floor(m.Phase * (1 << 32)))
		case notation.ModSway:
			spec.Kind, spec.Octaves = engine.ModKindSway, uint8(m.Octaves)
		case notation.ModEnvelope:
			spec.Kind, spec.Source = engine.ModKindEnvelope, uint8(trackIndex[m.Source])
			spec.Attack = followerCoefficient(m.AttackMS, cfg.SampleRate)
			spec.Release = followerCoefficient(m.ReleaseMS, cfg.SampleRate)
		case notation.ModContour:
			spec.Kind = engine.ModKindContour
			for _, pt := range m.Points {
				spec.Points = append(spec.Points, engine.ContourPoint{
					At:    uint64(math.Round(pt.At * (1 << 32))),
					Value: int32(math.Round(pt.Value * (1 << 30))), Shape: shapes[pt.Shape],
				})
			}
		}
		cfg.Modulators = append(cfg.Modulators, spec)
	}
	for _, r := range p.Routes {
		resolved, err := ResolveParameterPath(p, r.Target)
		if err != nil {
			return err
		}
		cfg.Routes = append(cfg.Routes, engine.RouteSpec{
			Source: uint8(index[r.Source]), Track: resolved.Track, Param: uint16(resolved.ID),
			Multiply: r.Mode == "multiply", Depth: float32(*r.Depth.Number),
		})
	}
	return nil
}

// followerCoefficient is the Q32 one-pole coefficient that moves a follower
// 1-1/e of the way to its input in ms, stepping once per 128-frame grid point.
func followerCoefficient(ms float64, sampleRate int) uint32 {
	if ms <= 0 {
		return math.MaxUint32
	}
	c := 1 - detmath.Exp(-128/(ms*.001*float64(sampleRate)))
	return uint32(min(math.Floor(c*(1<<32)), math.MaxUint32))
}

// routableRow explains why the engine cannot route to a row yet, or "".
func routableRow(id kernel.ParamID) string {
	if engine.RouteSupported(id) {
		return ""
	}
	return fmt.Sprintf("%s is not a row the engine routes yet; routes reach track level, pan, sends and the acid voice", kernel.Params[id].Path)
}
