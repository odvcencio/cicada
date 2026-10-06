package pro

// PresetNames is the stable audition order for the companion module and CLI.
var PresetNames = [...]string{"eq", "compressor", "transient", "width", "reverb", "limiter", "mastering"}

// Preset returns bounded demonstration controls. Loudness normalization remains
// a separate host operation; a mastering preset cannot promise live LUFS.
func Preset(name string) (Params, bool) {
	p := DefaultParams()
	switch name {
	case "eq":
		p.EQ = [4]Band{
			{Enabled: true, Type: Highpass, FrequencyHz: 30, Q: .7071067812},
			{Enabled: true, Type: LowShelf, FrequencyHz: 90, Q: 1, GainDB: 2},
			{Enabled: true, Type: Peak, FrequencyHz: 300, Q: 1, GainDB: -2},
			{Enabled: true, Type: HighShelf, FrequencyHz: 8000, Q: 1, GainDB: 1},
		}
	case "compressor":
		p.EnableCompressor = true
		p.Compressor.Threshold, p.Compressor.Ratio, p.Compressor.ReleaseMs = -18, 3, 120
		p.Compressor.MakeupAuto = false
	case "transient":
		p.Transient.Enabled, p.Transient.AttackDB, p.Transient.SustainDB = true, 4, -2
	case "width":
		p.Width.Enabled, p.Width.Amount, p.Width.BassMonoHz = true, 1.25, 120
	case "reverb":
		p.Reverb.Enabled, p.Reverb.EarlyMix, p.Reverb.Mix = true, .25, .22
		p.Reverb.Tail.DecaySec, p.Reverb.Tail.DampHz, p.Reverb.Tail.HighpassHz, p.Reverb.Tail.PredelayMs = 1.7, 7500, 100, 15
	case "limiter":
		p.EnableLimiter = true
	case "mastering":
		p.EQ = [4]Band{
			{Enabled: true, Type: Highpass, FrequencyHz: 25, Q: .7071067812},
			{Enabled: true, Type: LowShelf, FrequencyHz: 90, Q: 1, GainDB: 1.5},
			{Enabled: true, Type: Peak, FrequencyHz: 250, Q: .8, GainDB: -1.5},
			{Enabled: true, Type: HighShelf, FrequencyHz: 6500, Q: 1, GainDB: 1},
		}
		p.EnableCompressor, p.EnableLimiter = true, true
		p.Compressor.Threshold, p.Compressor.Ratio, p.Compressor.AttackMs, p.Compressor.ReleaseMs = -18, 1.8, 20, 160
		p.Compressor.MakeupAuto, p.Compressor.MakeupDB = false, 1
		p.Width.Enabled, p.Width.Amount, p.Width.BassMonoHz = true, 1.08, 100
	default:
		return Params{}, false
	}
	return p, true
}
