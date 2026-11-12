package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/seq"
)

// MeterFlag is header flags bit 7: an appended meter table follows every
// other table, whatever version the image has. A 4/4 song writes no table, so
// its image keeps its bytes and version. Unlike the modular tables, every
// build reads it.
const MeterFlag uint32 = 1 << 7

// maxMeterChanges bounds the table: one count byte.
const maxMeterChanges = 255

// writeMeters appends: u8 changes; per change u32 zero-based bar, u8 beats,
// u8 unit.
func writeMeters(w *writer, cfg *engine.Config) error {
	if len(cfg.Meters) > maxMeterChanges {
		return imageError("too many meter changes")
	}
	if _, err := configMeterMap(cfg); err != nil {
		return err
	}
	w.byte(byte(len(cfg.Meters)))
	for _, change := range cfg.Meters {
		w.u32(uint32(change.Bar))
		w.byte(byte(change.Meter.Beats))
		w.byte(byte(change.Meter.Unit))
	}
	return nil
}

func readMeters(r *reader, cfg *engine.Config) error {
	count, err := r.byte()
	if err != nil || count == 0 {
		return imageError("invalid meter table")
	}
	cfg.Meters = make([]seq.MeterChange, count)
	for i := range cfg.Meters {
		bar, err1 := r.u32()
		beats, err2 := r.byte()
		unit, err3 := r.byte()
		if err1 != nil || err2 != nil || err3 != nil {
			return imageError("invalid meter table")
		}
		cfg.Meters[i] = seq.MeterChange{Bar: int64(bar), Meter: seq.Meter{Beats: int64(beats), Unit: int64(unit)}}
	}
	_, err = configMeterMap(cfg)
	return err
}

// configMeterMap is the map a config's meters build: the zero map (4/4) for
// none.
func configMeterMap(cfg *engine.Config) (seq.MeterMap, error) {
	var m seq.MeterMap
	if len(cfg.Meters) == 0 {
		return m, nil
	}
	m, err := seq.NewMeterMap(cfg.Meters)
	if err != nil {
		return m, imageError("invalid meter table")
	}
	return m, nil
}
