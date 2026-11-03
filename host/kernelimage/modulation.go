package kernelimage

import (
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/engine"
)

// ModulatorFlag is header flags bit 1: an appended modulator table follows the
// last version table, whatever version the image has: no new image version.
const ModulatorFlag uint32 = 1 << 1

func hasModulation(cfg *engine.Config) bool { return len(cfg.Modulators) > 0 || len(cfg.Routes) > 0 }

// writeModulation appends: u8 sources, u8 routes; per source u8 kind, u8 wave,
// u8 octaves, u8 flags (bit 0 continue), u8 followed track, u32 rate ticks,
// u64 rate micro-hertz, u32 phase, u32 attack, u32 release, u8 points, and per
// point u64 at, i32 value, u8 shape; per route u8 source, u8 track, u16 row,
// u8 mode, f32 depth.
func writeModulation(w *writer, cfg *engine.Config) error {
	if len(cfg.Modulators) > engine.MaxModulators || len(cfg.Routes) > engine.MaxModRoutes {
		return imageError("too many modulators or routes")
	}
	w.byte(byte(len(cfg.Modulators)))
	w.byte(byte(len(cfg.Routes)))
	for _, m := range cfg.Modulators {
		if len(m.Points) > engine.MaxContourPoints {
			return imageError("too many contour points")
		}
		w.byte(m.Kind)
		w.byte(m.Wave)
		w.byte(m.Octaves)
		w.byte(boolByte(m.Continue))
		w.byte(m.Source)
		w.u32(m.RateTicks)
		w.u64(m.RateMicroHz)
		w.u32(m.PhaseQ32)
		w.u32(m.Attack)
		w.u32(m.Release)
		w.byte(byte(len(m.Points)))
		for _, p := range m.Points {
			w.u64(p.At)
			w.u32(uint32(p.Value))
			w.byte(p.Shape)
		}
	}
	for _, r := range cfg.Routes {
		w.byte(r.Source)
		w.byte(r.Track)
		w.u16(r.Param)
		w.byte(boolByte(r.Multiply))
		w.f32(r.Depth)
	}
	return nil
}

func readModulation(r *reader, cfg *engine.Config) error {
	sources, err := r.byte()
	if err != nil {
		return err
	}
	routes, err := r.byte()
	if err != nil {
		return err
	}
	if sources == 0 || int(sources) > engine.MaxModulators || int(routes) > engine.MaxModRoutes {
		return imageError("invalid modulator table")
	}
	cfg.Modulators = make([]engine.ModulatorSpec, sources)
	for i := range cfg.Modulators {
		m := &cfg.Modulators[i]
		var count byte
		var cont byte
		fields := []*byte{&m.Kind, &m.Wave, &m.Octaves, &cont, &m.Source}
		for _, f := range fields {
			if *f, err = r.byte(); err != nil {
				return err
			}
		}
		if cont > 1 {
			return imageError("invalid modulator flag")
		}
		m.Continue = cont == 1
		if m.RateTicks, err = r.u32(); err != nil {
			return err
		}
		if m.RateMicroHz, err = r.u64(); err != nil {
			return err
		}
		for _, f := range []*uint32{&m.PhaseQ32, &m.Attack, &m.Release} {
			if *f, err = r.u32(); err != nil {
				return err
			}
		}
		if count, err = r.byte(); err != nil {
			return err
		}
		if int(count) > engine.MaxContourPoints {
			return imageError("too many contour points")
		}
		if count > 0 {
			m.Points = make([]engine.ContourPoint, count)
		}
		for j := range m.Points {
			p := &m.Points[j]
			if p.At, err = r.u64(); err != nil {
				return err
			}
			v, err := r.u32()
			if err != nil {
				return err
			}
			p.Value = int32(v)
			if p.Shape, err = r.byte(); err != nil {
				return err
			}
		}
	}
	cfg.Routes = make([]engine.RouteSpec, routes)
	for i := range cfg.Routes {
		route := &cfg.Routes[i]
		var mode byte
		var row uint16
		if route.Source, err = r.byte(); err != nil {
			return err
		}
		if route.Track, err = r.byte(); err != nil {
			return err
		}
		if row, err = r.u16(); err != nil {
			return err
		}
		if mode, err = r.byte(); err != nil || mode > 1 {
			return imageError("invalid route mode")
		}
		route.Param, route.Multiply = row, mode == 1
		if route.Depth, err = r.f32(); err != nil {
			return err
		}
		if !engine.RouteSupported(kernel.ParamID(row)) {
			return imageError("route to an unsupported row")
		}
	}
	return nil
}
