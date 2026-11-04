package engine

import (
	"strings"
	"testing"

	"m31labs.dev/cicada/kernel/fx"
	"m31labs.dev/cicada/kernel/fx/chorus"
)

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
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("engine did not preflight: %v", err)
	}
	// Non-chorus projects keep their previous admission rules.
	for track := 0; track < cfg.Tracks; track++ {
		cfg.Track[track].InsertChain = nil
	}
	if err := ValidatePortableEffectBudget(&cfg); err != nil {
		t.Fatalf("changed legacy admission: %v", err)
	}
}
