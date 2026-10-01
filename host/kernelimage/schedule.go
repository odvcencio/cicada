package kernelimage

import (
	"m31labs.dev/cicada/kernel/engine"
	"math"
)

// Version 14 appends schedules and prepared PCM. Legacy projects continue
// encoding as version 13, so old fixtures and consumers stay byte-identical.
func writeSchedule(w *writer, cfg *engine.Config) error {
	if len(cfg.Schedule) > 65535 || len(cfg.Clips) > 65535 || len(cfg.Assets) > 65535 || len(cfg.Schedule) > 0 && len(cfg.Song) > 0 {
		return Error("invalid schedule image counts or authority")
	}
	needed := int64(16 + 26*len(cfg.Schedule) + 42*len(cfg.Clips))
	for _, a := range cfg.Assets {
		if a.SampleRate < 8000 || a.SampleRate > 192000 || len(a.Left) == 0 || len(a.Right) > 0 && len(a.Right) != len(a.Left) {
			return Error("invalid audio asset dimensions")
		}
		needed += 9 + 4*int64(len(a.Left)+len(a.Right))
	}
	if needed > int64(MaxImageBytes-len(w.data)) {
		return Error("project image exceeds 2 MiB")
	}
	w.f32(cfg.MasterBiasL)
	w.f32(cfg.MasterBiasR)
	w.u32(uint32(len(cfg.Schedule)))
	w.u16(uint16(len(cfg.Assets)))
	w.u16(uint16(len(cfg.Clips)))
	for _, v := range cfg.Schedule {
		w.u64(uint64(v.Tick))
		w.u64(uint64(v.EndTick))
		w.byte(byte(v.Kind))
		w.byte(v.Track)
		w.u16(v.Scene)
		w.u16(v.Index)
		w.u32(v.ID)
	}
	for _, a := range cfg.Assets {
		w.u32(uint32(a.SampleRate))
		w.u32(uint32(len(a.Left)))
		w.byte(boolByte(len(a.Right) > 0))
		for _, channel := range [][]float32{a.Left, a.Right} {
			for _, v := range channel {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					return Error("non-finite audio asset")
				}
				w.f32(v)
			}
		}
	}
	for _, c := range cfg.Clips {
		w.u16(c.Asset)
		w.u64(uint64(c.StartFrame))
		w.u64(uint64(c.EndFrame))
		w.u64(uint64(c.FadeInFrames))
		w.u64(uint64(c.FadeOutFrames))
		w.f64(c.GainDB)
	}
	return nil
}
func readSchedule(r *reader, cfg *engine.Config) error {
	var err error
	if cfg.MasterBiasL, err = r.f32(); err != nil {
		return err
	}
	if cfg.MasterBiasR, err = r.f32(); err != nil {
		return err
	}
	count, err := r.u32()
	if err != nil {
		return err
	}
	assets, err := r.u16()
	if err != nil {
		return err
	}
	clips, err := r.u16()
	if err != nil {
		return err
	}
	if count > 65535 || uint64(count)*26+uint64(assets)*9+uint64(clips)*42 > uint64(len(r.data)-r.at) {
		return Error("invalid schedule image counts")
	}
	cfg.Schedule = make([]engine.ScheduleEvent, int(count))
	cfg.Assets = make([]engine.AudioAsset, int(assets))
	cfg.Clips = make([]engine.ClipConfig, int(clips))
	for i := range cfg.Schedule {
		v := &cfg.Schedule[i]
		tick, err := r.u64()
		if err != nil {
			return err
		}
		v.Tick = int64(tick)
		end, err := r.u64()
		if err != nil {
			return err
		}
		v.EndTick = int64(end)
		kind, err := r.byte()
		if err != nil {
			return err
		}
		v.Kind = engine.ScheduleKind(kind)
		if v.Track, err = r.byte(); err != nil {
			return err
		}
		if v.Scene, err = r.u16(); err != nil {
			return err
		}
		if v.Index, err = r.u16(); err != nil {
			return err
		}
		if v.ID, err = r.u32(); err != nil {
			return err
		}
	}
	for i := range cfg.Assets {
		a := &cfg.Assets[i]
		rate, err := r.u32()
		if err != nil {
			return err
		}
		frames, err := r.u32()
		if err != nil {
			return err
		}
		stereo, err := r.byte()
		if err != nil || stereo > 1 {
			return Error("invalid asset image channels")
		}
		if frames == 0 || uint64(frames)*4*uint64(1+stereo) > uint64(len(r.data)-r.at) {
			return Error("invalid asset image frames")
		}
		a.SampleRate = int(rate)
		a.Left = make([]float32, int(frames))
		if stereo == 1 {
			a.Right = make([]float32, int(frames))
		}
		for _, channel := range [][]float32{a.Left, a.Right} {
			for j := range channel {
				value, err := r.f32()
				if err != nil {
					return err
				}
				if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
					return Error("non-finite asset image")
				}
				channel[j] = value
			}
		}
	}
	for i := range cfg.Clips {
		c := &cfg.Clips[i]
		if c.Asset, err = r.u16(); err != nil {
			return err
		}
		for _, field := range []*int64{&c.StartFrame, &c.EndFrame, &c.FadeInFrames, &c.FadeOutFrames} {
			value, err := r.u64()
			if err != nil {
				return err
			}
			*field = int64(value)
		}
		if c.GainDB, err = r.f64(); err != nil {
			return err
		}
	}
	return nil
}
