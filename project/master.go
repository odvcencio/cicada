package project

import (
	"fmt"
	"math"
	"os"
	"strings"

	"m31labs.dev/cicada/host/irasset"
	"m31labs.dev/cicada/kernel/engine"
	"m31labs.dev/cicada/kernel/fx/convolution"
	"m31labs.dev/cicada/kernel/fx/pro"
	"m31labs.dev/cicada/kernel/mix"
)

// MasterHasInsert distinguishes host-owned master effects from kernel returns.
func MasterHasInsert(p *Project, id string) bool {
	if p.Master != nil {
		for _, name := range p.Master.Mixer.Inserts {
			if name == id {
				return true
			}
		}
	}
	return false
}

type masterChain struct{ stages []engine.StereoProcessor }

func (c *masterChain) Process(l, r float32) (float32, float32) {
	for _, stage := range c.stages {
		l, r = stage.Process(l, r)
	}
	return l, r
}
func (c *masterChain) Reset() {
	for _, stage := range c.stages {
		stage.Reset()
	}
}
func (c *masterChain) Fault() bool {
	for _, stage := range c.stages {
		if stage.Fault() {
			return true
		}
	}
	return false
}
func (c *masterChain) LatencyFrames() int {
	frames := 0
	for _, stage := range c.stages {
		frames += stage.LatencyFrames()
	}
	return frames
}

// PrepareMaster owns all DSP storage before rendering. Each declared effect
// is a separate stage, so source order is preserved even for repeated kinds.
func PrepareMaster(p *Project, rate int) (engine.StereoProcessor, error) {
	if p.Master == nil || len(p.Master.Mixer.Inserts) == 0 {
		return nil, nil
	}
	c := &masterChain{}
	for _, name := range p.Master.Mixer.Inserts {
		var effect Effect
		for _, candidate := range p.Effects {
			if candidate.ID == name {
				effect = candidate
				break
			}
		}
		if effect.ID == "" {
			return nil, fmt.Errorf("CICADA-REFERENCE: unknown master effect %s", name)
		}
		var stage engine.StereoProcessor
		var err error
		if semanticEffectKind(effect) == "convolution" {
			stage, err = prepareMasterConvolution(p, effect, rate)
		} else {
			var params pro.Params
			params, err = masterEffectParams(effect)
			if err == nil {
				stage, err = pro.New(rate, params)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("master effect %s: %w", name, err)
		}
		c.stages = append(c.stages, stage)
	}
	return c, nil
}

func masterEffectParams(effect Effect) (pro.Params, error) {
	p := pro.DefaultParams()
	kind := semanticEffectKind(effect)
	if kind == "comp" {
		params, sidechain, err := CompSpecFromValues(effect.Params)
		if err == nil && sidechain != "" {
			err = fmt.Errorf("CICADA-UNSUPPORTED: master compressor uses its own input; external sidechain is unavailable")
		}
		p.EnableCompressor, p.Compressor = true, params
		return p, err
	}
	p.EQ[0] = pro.Band{Enabled: true, Type: pro.Peak, FrequencyHz: 1000, Q: 1}
	p.Transient.Enabled, p.Width.Enabled, p.EnableLimiter = kind == "transient", kind == "width", kind == "limiter"
	if kind != "eq" {
		p.EQ[0].Enabled = false
	}
	for _, name := range sortedKeys(effect.Params) {
		v := effect.Params[name]
		var dst *float64
		unit := "unit"
		switch kind + "." + name {
		case "eq.type":
			if v.Number != nil || v.Unit != "enum" {
				return p, fmt.Errorf("EQ type requires peak, low_shelf, high_shelf, highpass, or lowpass")
			}
			types := map[string]pro.EQType{"peak": pro.Peak, "low_shelf": pro.LowShelf, "high_shelf": pro.HighShelf, "highpass": pro.Highpass, "lowpass": pro.Lowpass}
			t, ok := types[v.Text]
			if !ok {
				return p, fmt.Errorf("unknown EQ type %s", v.Text)
			}
			p.EQ[0].Type = t
			continue
		case "eq.frequency":
			dst, unit = &p.EQ[0].FrequencyHz, "hz"
		case "eq.q":
			dst = &p.EQ[0].Q
		case "eq.gain":
			dst, unit = &p.EQ[0].GainDB, "db"
		case "transient.attack":
			dst, unit = &p.Transient.AttackDB, "db"
		case "transient.sustain":
			dst, unit = &p.Transient.SustainDB, "db"
		case "transient.fast":
			dst, unit = &p.Transient.FastMs, "ms"
		case "transient.slow":
			dst, unit = &p.Transient.SlowMs, "ms"
		case "transient.release":
			dst, unit = &p.Transient.ReleaseMs, "ms"
		case "width.amount":
			dst = &p.Width.Amount
		case "width.bass_mono":
			dst, unit = &p.Width.BassMonoHz, "hz"
		case "limiter.ceiling":
			dst, unit = &p.Limiter.CeilingDBTP, "dbtp"
		case "limiter.lookahead":
			dst, unit = &p.Limiter.LookaheadMs, "ms"
		case "limiter.release":
			dst, unit = &p.Limiter.ReleaseMs, "ms"
		default:
			return p, fmt.Errorf("CICADA-PARAM: unknown %s parameter %s", kind, name)
		}
		if v.Number == nil || v.Text != "" || v.Unit != unit || math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0) {
			return p, fmt.Errorf("CICADA-PARAM: %s %s requires %s", kind, name, unit)
		}
		*dst = *v.Number
	}
	if kind != "eq" && kind != "transient" && kind != "width" && kind != "limiter" {
		return p, fmt.Errorf("CICADA-UNSUPPORTED: %s as a master insert is not implemented", kind)
	}
	return p, nil
}

func convolutionSpec(effect Effect) (asset string, amount float64, partition int, err error) {
	amount, partition = .15, 128
	for _, name := range sortedKeys(effect.Params) {
		v := effect.Params[name]
		switch name {
		case "asset":
			if v.Unit != "enum" || v.Number != nil || !validID(v.Text) {
				err = fmt.Errorf("convolution asset requires an asset name")
			} else {
				asset = v.Text
			}
		case "mix":
			if v.Unit != "unit" || v.Number == nil || math.IsNaN(*v.Number) || *v.Number < 0 || *v.Number > 1 {
				err = fmt.Errorf("convolution mix must be 0 to 1")
			} else {
				amount = *v.Number
			}
		case "partition":
			if v.Unit != "frames" || v.Number == nil || math.IsNaN(*v.Number) || *v.Number < 64 || *v.Number > 2048 || math.Trunc(*v.Number) != *v.Number {
				err = fmt.Errorf("convolution partition requires 64 to 2048 frames")
			} else {
				partition = int(*v.Number)
				if partition&(partition-1) != 0 {
					err = fmt.Errorf("convolution partition must be a power of two")
				}
			}
		default:
			err = fmt.Errorf("unknown convolution parameter %s", name)
		}
		if err != nil {
			return
		}
	}
	if asset == "" {
		err = fmt.Errorf("convolution requires an asset")
	}
	return
}

type masterConvolution struct {
	wet    *convolution.Reverb
	dry    *mix.Delay
	amount float32
}

func (c *masterConvolution) Process(l, r float32) (float32, float32) {
	wl, wr := c.wet.Process(l, r)
	dl, dr := c.dry.Process(l, r)
	return dl*(1-c.amount) + wl*c.amount, dr*(1-c.amount) + wr*c.amount
}
func (c *masterConvolution) Reset()             { c.wet.Reset(); c.dry.Reset() }
func (c *masterConvolution) Fault() bool        { return c.wet.Fault() }
func (c *masterConvolution) LatencyFrames() int { return c.wet.LatencyFrames() }

func prepareMasterConvolution(p *Project, effect Effect, rate int) (engine.StereoProcessor, error) {
	name, amount, partition, err := convolutionSpec(effect)
	if err != nil {
		return nil, err
	}
	var asset Asset
	for _, a := range p.Assets {
		if a.Name == name {
			asset = a
			break
		}
	}
	if asset.Name == "" {
		return nil, fmt.Errorf("CICADA-REFERENCE: unknown convolution asset %s", name)
	}
	root, err := os.OpenRoot(asset.Directory("."))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(asset.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	impulse, err := irasset.DecodeWAV(f, asset.SHA256, asset.Frames, asset.RateHz, asset.Channels, rate)
	if err != nil {
		return nil, err
	}
	impulse, err = impulse.Condition(20, 1)
	if err != nil {
		return nil, err
	}
	wet, err := impulse.Convolver(partition)
	if err != nil {
		return nil, err
	}
	dry, err := mix.NewDelay(wet.LatencyFrames())
	if err != nil {
		return nil, err
	}
	return &masterConvolution{wet: wet, dry: dry, amount: float32(amount)}, nil
}

func validateMasterEffect(p *Project, effect Effect) error {
	if semanticEffectKind(effect) == "convolution" {
		name, _, _, err := convolutionSpec(effect)
		if err != nil {
			return err
		}
		for _, asset := range p.Assets {
			if asset.Name == name {
				if asset.Frames > convolution.MaxFrames || asset.Frames > int64(asset.RateHz)*12 {
					return fmt.Errorf("CICADA-LIMIT: impulse exceeds 12 seconds or resident frame limit")
				}
				return nil
			}
		}
		return fmt.Errorf("CICADA-REFERENCE: unknown convolution asset %s", name)
	}
	params, err := masterEffectParams(effect)
	if err != nil {
		return err
	}
	_, err = pro.New(48000, params)
	if err != nil {
		return fmt.Errorf("CICADA-PARAM: %w", err)
	}
	return nil
}

func masterDiagnosticCode(err error) string {
	for _, code := range []string{"CICADA-REFERENCE", "CICADA-LIMIT", "CICADA-UNSUPPORTED", "CICADA-DUPLICATE"} {
		if strings.Contains(err.Error(), code+":") {
			return code
		}
	}
	return "CICADA-PARAM"
}
