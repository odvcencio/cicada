package kernelimage

import "m31labs.dev/cicada/kernel/engine"

// MacroMappingFlag marks the appended parameter-mapped macro table.
const MacroMappingFlag uint32 = 1 << 2

func writeMacroMappings(w *writer, cfg *engine.Config) error {
	if len(cfg.MacroMappings) == 0 || len(cfg.MacroMappings) > 16 {
		return imageError("invalid mapped macro count")
	}
	w.byte(byte(len(cfg.MacroMappings)))
	for _, m := range cfg.MacroMappings {
		w.byte(m.Macro)
		w.byte(m.Track)
		w.u16(m.Param)
		w.f32(m.Min)
		w.f32(m.Max)
		w.byte(boolByte(m.Exponential))
	}
	return nil
}

func readMacroMappings(r *reader, cfg *engine.Config) error {
	count, err := r.byte()
	if err != nil || count == 0 || count > 16 {
		return imageError("invalid mapped macro count")
	}
	cfg.MacroMappings = make([]engine.MacroMapping, count)
	for i := range cfg.MacroMappings {
		m := &cfg.MacroMappings[i]
		if m.Macro, err = r.byte(); err != nil {
			return err
		}
		if m.Track, err = r.byte(); err != nil {
			return err
		}
		if m.Param, err = r.u16(); err != nil {
			return err
		}
		if m.Min, err = r.f32(); err != nil {
			return err
		}
		if m.Max, err = r.f32(); err != nil {
			return err
		}
		curve, err := r.byte()
		if err != nil || curve > 1 {
			return imageError("invalid mapped macro curve")
		}
		m.Exponential = curve == 1
	}
	return nil
}
