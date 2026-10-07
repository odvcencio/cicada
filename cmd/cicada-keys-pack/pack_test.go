package main

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"m31labs.dev/cicada/host/instrumentpack"
	"m31labs.dev/cicada/host/keyboard"
)

func optionsFor(patch string, duration float64) options {
	o := defaults()
	o.Patch, o.Low, o.High, o.Duration = patch, 60, 63, duration
	return o
}

func TestDimensionsAndBudget(t *testing.T) {
	layers := velocityLayers(5)
	for i, expected := range []int{25, 51, 76, 102, 127} {
		if layers[i].Center != expected || layers[i].Low > expected || layers[i].High < expected {
			t.Fatalf("invalid layer: %+v", layers[i])
		}
		if i > 0 && layers[i-1].High+1 != layers[i].Low {
			t.Fatal("velocity ranges overlap or have a gap")
		}
	}
	if layers[0].Low != 1 || layers[4].High != 127 {
		t.Fatal("velocity coverage is incomplete")
	}
	for _, step := range []int{1, 3} {
		zones := rootZones(21, 108, step)
		if zones[0].Low != 21 || zones[len(zones)-1].High != 108 {
			t.Fatal("key coverage is incomplete")
		}
		for i, zone := range zones {
			if zone.Note < zone.Low || zone.Note > zone.High {
				t.Fatal("root outside key zone")
			}
			if i > 0 && zones[i-1].High+1 != zone.Low {
				t.Fatal("key ranges overlap or have a gap")
			}
		}
	}
	plans, err := planPacks(defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != len(keyboard.Names) {
		t.Fatalf("planned %d patches", len(plans))
	}
	for _, p := range plans {
		if p.PCMBytes > maxPCMBytes {
			t.Fatalf("%s exceeds decoded budget", p.Patch)
		}
		if len(p.Roots) != 30 || len(p.Layers) != 5 {
			t.Fatal("default grid changed")
		}
	}
	chromatic := defaults()
	chromatic.Patch, chromatic.Step = "tine_ep", 1
	if _, err = planPacks(chromatic); err == nil || !strings.Contains(err.Error(), "256 MiB") {
		t.Fatalf("oversized request not rejected: %v", err)
	}
	for _, mutate := range []func(*options){
		func(o *options) { o.Layers = 4 }, func(o *options) { o.Layers = 17 }, func(o *options) { o.RoundRobins = 1 }, func(o *options) { o.Step = 2 }, func(o *options) { o.Rate = 44100 }, func(o *options) { o.Duration = math.NaN() }, func(o *options) { o.Low = 20 },
	} {
		o := defaults()
		mutate(&o)
		if _, err := planPacks(o); err == nil {
			t.Fatalf("invalid dimensions admitted: %+v", o)
		}
	}
}

func TestDecimatorPassbandAndStopband(t *testing.T) {
	filter := newDecimator()
	const frames = 4800
	measure := func(frequency float64) float64 {
		source := make([]float32, frames*4+filterHalf)
		for i := range source {
			source[i] = float32(math.Sin(2 * math.Pi * frequency * float64(i) / modelRate))
		}
		pcm := filter.downsample(source, frames)
		var energy float64
		for _, x := range pcm[480:] {
			energy += float64(x) * float64(x)
		}
		return math.Sqrt(energy/float64(len(pcm)-480)) * math.Sqrt(2)
	}
	pass, stop := measure(1000), measure(28000)
	if math.Abs(pass-1) > .00001 || stop > .0001 {
		t.Fatalf("filter pass=%g stop=%g", pass, stop)
	}
	t.Logf("1kHz_gain=%.9f 28kHz_rejection=%.2f dB", pass, 20*math.Log10(stop))
}

func generate(t testing.TB, o options) (string, *instrumentpack.Prepared) {
	t.Helper()
	root := t.TempDir()
	o.Out = root
	plans, err := planPacks(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = preflightOutput(root, plans); err != nil {
		t.Fatal(err)
	}
	filter := newDecimator()
	for _, p := range plans {
		if _, err = buildPack(o, p, filter, io.Discard); err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(root, o.Patch)
	pin, err := os.ReadFile(filepath.Join(dir, "manifest.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := instrumentpack.Load(dir, "manifest.json", strings.TrimSpace(string(pin)))
	if err != nil {
		t.Fatal(err)
	}
	return dir, prepared
}

func TestOwnedPackLoadsLayersRoundRobinsAndReleases(t *testing.T) {
	dir, pack := generate(t, optionsFor("tine_ep", .25))
	if pack.Manifest.Format != instrumentpack.Format || pack.Manifest.Config.Voices != 8 || len(pack.Manifest.Assets) != 40 || len(pack.Zones) != 40 {
		t.Fatal("pack dimensions or format changed")
	}
	attack, release := 0, 0
	for _, zone := range pack.Zones {
		if zone.Region.Loop || zone.Gain >= 16 || zone.Layer < zone.VelocityLow || zone.Layer > zone.VelocityHigh || zone.Count != 2 {
			t.Fatal("invalid bounded zone")
		}
		if zone.Release {
			release++
		} else {
			attack++
		}
		if math.Abs(zone.Gain*float64(zone.Layer)/127-1) > .000001 {
			t.Fatal("velocity gain is not compensated")
		}
	}
	if attack != 20 || release != 20 {
		t.Fatalf("attacks=%d releases=%d", attack, release)
	}
	for _, asset := range pack.Manifest.Assets {
		if asset.Channels != 1 || asset.Rate != outputRate || asset.License != "CC0-1.0" || asset.SourceURL != sourceURL {
			t.Fatal("own audio dimensions or provenance changed")
		}
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(asset.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if !canonicalGzipHeader(data) {
			t.Fatal("gzip carries ambient metadata")
		}
	}
	if pack.Manifest.Assets[0].SHA256 == pack.Manifest.Assets[2].SHA256 {
		t.Fatal("round robin physical variation did not change the take")
	}
	voice, err := pack.New(outputRate)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := voice.NoteOn(60, 76)
	if err != nil {
		t.Fatal(err)
	}
	for range 4000 {
		voice.NextStereo()
	}
	if !voice.NoteOff(handle) {
		t.Fatal("sampled note release rejected")
	}
	var energy float64
	for range 4800 {
		l, r := voice.NextStereo()
		if !finitePCM(l) || !finitePCM(r) {
			t.Fatal("release is nonfinite")
		}
		energy += float64(l)*float64(l) + float64(r)*float64(r)
	}
	if energy == 0 {
		t.Fatal("recorded release is silent")
	}
}

func TestGeneratedBytesAreReproducible(t *testing.T) {
	a, _ := generate(t, optionsFor("clav_muted", .08))
	b, _ := generate(t, optionsFor("clav_muted", .08))
	err := filepath.WalkDir(a, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(a, path)
		if err != nil {
			return err
		}
		left, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		right, err := os.ReadFile(filepath.Join(b, relative))
		if err != nil {
			return err
		}
		if !bytes.Equal(left, right) {
			t.Fatalf("nonreproducible generated file %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReleaseIsCapturedBeforeShortVoiceExpires(t *testing.T) {
	for _, patch := range []string{"clav_muted", "reed_ep"} {
		o := optionsFor(patch, 4)
		o.Low, o.High = 108, 108
		plans, err := planPacks(o)
		if err != nil {
			t.Fatal(err)
		}
		filter := newDecimator()
		_, release, err := captureTake(plans[0], 108, 102, 0, &filter)
		if err != nil {
			t.Fatal(err)
		}
		var energy float64
		for _, x := range release.Left {
			energy += float64(x) * float64(x)
		}
		if energy < .00000001 {
			t.Fatalf("%s release disappeared after long held capture: energy=%g", patch, energy)
		}
		t.Logf("patch=%s note=108 release_energy=%.9f", patch, energy)
	}
}

func TestModeledVersusReplayedRootLayer(t *testing.T) {
	o := optionsFor("tine_ep", .25)
	_, pack := generate(t, o)
	plans, err := planPacks(o)
	if err != nil {
		t.Fatal(err)
	}
	filter := newDecimator()
	for _, velocity := range []int{25, 51, 76, 102, 127} {
		modeled, _, err := captureTake(plans[0], 60, velocity, 0, &filter)
		if err != nil {
			t.Fatal(err)
		}
		voice, err := pack.New(outputRate)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = voice.NoteOn(60, uint8(velocity)); err != nil {
			t.Fatal(err)
		}
		nativeSpec, err := keyboard.DefaultSpec("tine_ep")
		if err != nil {
			t.Fatal(err)
		}
		native, err := keyboard.New(outputRate, &nativeSpec)
		if err != nil {
			t.Fatal(err)
		}
		if err = native.NoteOn(60, uint8(velocity)); err != nil {
			t.Fatal(err)
		}
		var signal, errorAll, errorBody, maximum, nativeSignal, nativeError float64
		for i := 0; i < 6000; i++ {
			actual, _ := voice.NextStereo()
			nativeL, _ := native.NextStereo()
			expected := float64(modeled.Left[i])
			difference := float64(actual) - expected
			signal += expected * expected
			errorAll += difference * difference
			if i >= 96 {
				errorBody += difference * difference
				maximum = max(maximum, math.Abs(difference))
				nativeSignal += float64(nativeL) * float64(nativeL)
				nativeDifference := float64(actual) - float64(nativeL)
				nativeError += nativeDifference * nativeDifference
			}
		}
		if maximum > .00000015 || math.Sqrt(errorBody/max(signal, 1e-30)) > .0001 {
			t.Fatalf("velocity=%d root/layer replay error peak=%g", velocity, maximum)
		}
		t.Logf("root=60 velocity=%d first125ms_nrmse=%.8f after2ms_nrmse=%.8f peak_body_error=%.9f native48k_default_after2ms_nrmse=%.6f", velocity, math.Sqrt(errorAll/max(signal, 1e-30)), math.Sqrt(errorBody/max(signal, 1e-30)), maximum, math.Sqrt(nativeError/max(nativeSignal, 1e-30)))
	}
}

func TestLoopRetainsAttackAndSurvivesWrap(t *testing.T) {
	dir, pack := generate(t, optionsFor("tonewheel_organ", 1))
	for _, zone := range pack.Zones {
		if !zone.Region.Loop || zone.Region.LoopStart < outputRate*3/4 || zone.Region.Crossfade < outputRate*40/1000 {
			t.Fatal("loop discarded the attack or crossfade")
		}
	}
	voice, err := pack.New(outputRate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = voice.NoteOn(60, 102); err != nil {
		t.Fatal(err)
	}
	var lateEnergy, seamJump, previous float64
	for i := range outputRate * 2 {
		l, r := voice.NextStereo()
		if !finitePCM(l) || !finitePCM(r) || math.Abs(float64(l)) > 2 || math.Abs(float64(r)) > 2 {
			t.Fatal("invalid looped output")
		}
		if i >= outputRate {
			lateEnergy += float64(l)*float64(l) + float64(r)*float64(r)
		}
		if i == outputRate {
			seamJump = math.Abs(float64(l) - previous)
		}
		previous = float64(l)
	}
	if lateEnergy < .01 {
		t.Fatal("looped instrument ended with its finite capture")
	}
	var naturalJump float64
	for _, zone := range pack.Zones {
		if zone.Region.RootKey != 60 || zone.Layer != 102 || zone.Position != 0 {
			continue
		}
		for i := zone.Region.LoopStart + 1; i < zone.Region.LoopEnd; i++ {
			naturalJump = max(naturalJump, math.Abs(float64(zone.Region.Left[i]-zone.Region.Left[i-1])))
		}
		break
	}
	if seamJump > 2*naturalJump+.000001 {
		t.Fatalf("loop seam jump=%g exceeds natural adjacent-frame difference=%g", seamJump, naturalJump)
	}
	data, err := os.ReadFile(filepath.Join(dir, "render-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report packSummary
	if err = json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.LoopSeamErrorMax <= 0 || report.LoopSeamErrorMax > 1.5 {
		t.Fatalf("unexpected seam score=%g", report.LoopSeamErrorMax)
	}
	t.Logf("loop_seam_normalized_error_min=%.6f max=%.6f decoded_pcm=%d actual_seam_jump=%.6f natural_adjacent_jump=%.6f", report.LoopSeamErrorMin, report.LoopSeamErrorMax, report.DecodedPCMBytes, seamJump, naturalJump)
}

func TestRemainingFamiliesLoadPinnedPacks(t *testing.T) {
	for _, test := range []struct {
		patch    string
		duration float64
	}{
		{"tine_tremolo", .08},
		{"organ_jazz", 2},
		{"fm_ep", .08},
		{"brass_stab", .08},
		{"soft_pad", 1.5},
		{"string_machine", .6},
	} {
		t.Run(test.patch, func(t *testing.T) {
			o := optionsFor(test.patch, test.duration)
			o.High = o.Low
			_, pack := generate(t, o)
			voice, err := pack.New(outputRate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = voice.NoteOn(60, 102); err != nil {
				t.Fatal(err)
			}
			var peak float64
			for range min(int(test.duration*outputRate), outputRate) {
				l, r := voice.NextStereo()
				if !finitePCM(l) || !finitePCM(r) {
					t.Fatal("nonfinite replay")
				}
				peak = max(peak, math.Abs(float64(l)), math.Abs(float64(r)))
			}
			if peak < .000001 {
				t.Fatal("captured patch replays silence")
			}
		})
	}
}

func TestCLIAndOutputSafety(t *testing.T) {
	var output, errors bytes.Buffer
	if err := run([]string{"--estimate-only", "--patch", "tine_ep", "--low", "60", "--high", "63", "--duration", ".1"}, &output, &errors); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "2 roots") {
		t.Fatal("missing estimate")
	}
	if err := run([]string{"--patch", "missing"}, io.Discard, io.Discard); err == nil {
		t.Fatal("unknown patch admitted")
	}
	root := t.TempDir()
	plans, err := planPacks(optionsFor("clav", .1))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "clav"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = preflightOutput(root, plans); err == nil {
		t.Fatal("existing directory would be replaced")
	}
}
