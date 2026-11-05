package engine

import (
	"strings"
	"testing"
	"unsafe"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/chorus"
	"m31labs.dev/cicada/kernel/modulate"
)

type budgetProbeFactory struct{ calls *int }

func (f budgetProbeFactory) VoiceCount() int { return 1 }
func (f budgetProbeFactory) NewStereoVoice(int) (StereoVoice, error) {
	*f.calls++
	return nil, nil
}

func TestPortableChorusControlReserveCoversBoundedTables(t *testing.T) {
	bound := unsafe.Sizeof(modState{}) + MaxModulators*(unsafe.Sizeof(ModulatorSpec{})+unsafe.Sizeof([]modulate.Point{})+8) + MaxModulators*MaxContourPoints*(unsafe.Sizeof(modulate.Point{})+unsafe.Sizeof(ContourPoint{})) + MaxModRoutes*(unsafe.Sizeof(RouteSpec{})+unsafe.Sizeof(modTarget{})+1) + 16*unsafe.Sizeof(MacroMapping{})
	if bound > 128<<10 {
		t.Fatalf("control tables require %d bytes, above reserved 128 KiB", bound)
	}
}

func TestPortableChorusBudgetBeforeEffectConstruction(t *testing.T) {
	cfg := Config{SampleRate: 96000, MaxBlock: 128, Tracks: 16, MaxVoices: 16, BPMMilli: 120000, PortableEffectsBudget: true}
	p := chorus.DefaultParams()
	stage := TrackEffectConfig{Kind: TrackEffectChorus, Chorus: &p}
	for track := 0; track < cfg.Tracks; track++ {
		cfg.Track[track].Kind = VoiceAcid
		cfg.Track[track].InsertChain = make([]TrackEffectConfig, MaxTrackInserts)
		for i := range cfg.Track[track].InsertChain {
			cfg.Track[track].InsertChain[i] = stage
		}
	}
	if err := ValidatePortableEffectBudget(&cfg); err != nil {
		t.Fatalf("choruses alone should fit: %v", err)
	}
	delay := fx.DefaultDelayParams()
	cfg.DelayA = &delay
	if err := ValidatePortableEffectBudget(&cfg); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("over-budget effects: %v", err)
	}
	var calls int
	cfg.Track[0].Kind, cfg.Track[0].Prepared = VoicePrepared, budgetProbeFactory{calls: &calls}
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("engine did not preflight: %v", err)
	}
	if calls != 0 {
		t.Fatal("constructed a voice before rejecting the effect budget")
	}
	// Non-chorus projects keep their previous admission rules.
	for track := 0; track < cfg.Tracks; track++ {
		cfg.Track[track].InsertChain = nil
	}
	if err := ValidatePortableEffectBudget(&cfg); err != nil {
		t.Fatalf("changed legacy admission: %v", err)
	}
}
