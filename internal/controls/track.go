package controls

import (
	"fmt"
	"math"
	"strings"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/voice/keyboard"
	"m31labs.dev/cicada/kernel/voice/modal"
	"m31labs.dev/cicada/notation"
)

// Value sources of a track control's current value.
const (
	FromTrack      = "track"      // the track block sets it
	FromPreset     = "preset"     // a preset the track plays sets it
	FromInstrument = "instrument" // the instrument declaration sets it
	FromDefault    = "default"    // nothing sets it; the voice's default applies
)

// TrackControl is a control of one track in a score, with its current value.
type TrackControl struct {
	Control
	// Value is the current value: a number in the control's unit, a word, or
	// null when nothing sets it and it has no single default.
	Value any `json:"value"`
	// Written is the source text that sets it, when something does.
	Written     string `json:"written,omitempty"`
	ValueSource string `json:"value_source"`
	// Overridden is true when the track block sets the control.
	Overridden bool `json:"overridden"`
}

// TrackView is what one track's voice accepts.
type TrackView struct {
	Track string `json:"track"`
	// Kind is the track's kind as the score writes it; Preset names the
	// preset it plays when Kind is one.
	Kind   string `json:"kind"`
	Preset string `json:"preset,omitempty"`
	// Owner is the catalog owner of the voice: a voice, patch, kit_lane,
	// sampler or authored (an instrument graph declared in the score).
	Owner     string `json:"owner"`
	OwnerKind string `json:"owner_kind"`
	// Instrument names the instrument declaration of an authored voice.
	Instrument string         `json:"instrument,omitempty"`
	Controls   []TrackControl `json:"controls"`
	Inserts    []Insert       `json:"inserts"`
}

// Insert is an effect in the track's insert chain.
type Insert struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ErrUnknownTrack is returned when the score has no such track.
type ErrUnknownTrack struct{ Track string }

func (e ErrUnknownTrack) Error() string { return fmt.Sprintf("score has no track %q", e.Track) }

// ForTrack lists the controls of one track: its voice's controls first, then
// the mixer strip's, each with its current value.
func ForTrack(score *notation.Score, name string) (TrackView, error) {
	var track *notation.Track
	for i := range score.Tracks {
		if score.Tracks[i].Name == name {
			track = &score.Tracks[i]
			break
		}
	}
	if track == nil {
		return TrackView{}, ErrUnknownTrack{name}
	}
	view := TrackView{Track: track.Name, Kind: track.Kind, Controls: []TrackControl{}, Inserts: []Insert{}}
	kind := track.Kind
	var preset []notation.Param
	if p, ok := notation.FindPreset(score, kind); ok {
		view.Preset, preset = p.Name, p.Params
		kind = strings.TrimPrefix(p.Target, "builtin.")
	}
	owner, voice := voiceControls(score, kind)
	view.Owner, view.OwnerKind = owner.Name, owner.Kind
	if owner.Kind == KindAuthored {
		view.Instrument = kind
	}
	var declared []notation.InstrumentParam
	for _, inst := range score.Instruments {
		if inst.Name == kind {
			declared = inst.Params
		}
	}
	for _, c := range voice {
		tc := TrackControl{Control: c, Value: c.Default, ValueSource: FromDefault}
		for _, p := range declared {
			if p.Name == c.Name {
				tc.Value, tc.Written, tc.ValueSource = sourceValue(c, p.Default), p.Default, FromInstrument
			}
		}
		for _, p := range preset {
			if p.Name == c.Name {
				tc.Value, tc.Written, tc.ValueSource = sourceValue(c, p.Value), p.Value, FromPreset
			}
		}
		for _, p := range track.Params {
			if p.Name == c.Name || contains(c.Aliases, p.Name) {
				tc.Value, tc.Written, tc.ValueSource, tc.Overridden = sourceValue(c, p.Value), p.Value, FromTrack, true
			}
		}
		view.Controls = append(view.Controls, tc)
	}
	effects := map[string]string{}
	for _, e := range score.Effects {
		effects[e.Name] = e.Kind
	}
	for _, c := range All().Of("mix") {
		tc := TrackControl{Control: c, Value: c.Default, ValueSource: FromDefault}
		for _, p := range track.Params {
			match := p.Name == c.Name || contains(c.Aliases, p.Name)
			if p.Name == "send" {
				match = c.Name == "send."+effects[p.Target]
			}
			if match {
				tc.Value, tc.Written, tc.ValueSource, tc.Overridden = sourceValue(c, p.Value), p.Value, FromTrack, true
			}
			if p.Name == "insert" {
				for _, n := range strings.Fields(p.Value) {
					if n != "->" && n != "none" && c.Name == "insert" {
						view.Inserts = appendInsert(view.Inserts, Insert{Name: n, Kind: effects[n]})
					}
				}
			}
		}
		view.Controls = append(view.Controls, tc)
	}
	return view, nil
}

func appendInsert(list []Insert, insert Insert) []Insert {
	for _, existing := range list {
		if existing.Name == insert.Name {
			return list
		}
	}
	return append(list, insert)
}

// voiceControls finds the owner of a track kind and the controls its track
// block takes, following notation's order: a declared instrument, kit or
// sampler shadows a built-in of the same name.
func voiceControls(score *notation.Score, kind string) (Owner, []Control) {
	catalog := All()
	for _, inst := range score.Instruments {
		if inst.Name != kind {
			continue
		}
		owner, _ := catalog.Owner("authored")
		var out []Control
		for slot, p := range inst.Params {
			meaning := instrument.ParameterMeaning(p.Name)
			if meaning == "" {
				meaning = "a parameter instrument " + inst.Name + " declares"
			}
			c := AuthoredParam("authored", slot, p, meaning)
			c.ID = "authored." + c.Name
			c.Example = Example(c)
			out = append(out, c)
		}
		for _, c := range catalog.Of("authored") {
			if !declares(inst, c.Name) {
				out = append(out, c)
			}
		}
		return owner, out
	}
	for _, kit := range score.Kits {
		if kit.Name != kind {
			continue
		}
		owner, _ := catalog.Owner("kit_lane")
		var out []Control
		for _, binding := range kit.Bindings {
			modeled := strings.HasPrefix(binding.Target, "model.")
			sampled := false
			if at := strings.LastIndex(binding.Target, "."); at >= 0 && !modeled && !strings.HasPrefix(binding.Target, "builtin.") {
				_, sampled = notation.ParseSamplerPitch(binding.Target[at+1:])
			}
			for _, c := range catalog.Of("kit_lane") {
				if !modeled && !(sampled && (c.Name == "level" || c.Name == "pan")) {
					continue
				}
				lane := c
				lane.Name = binding.Lane + "_" + c.Name
				lane.ID = "kit_lane." + lane.Name
				lane.descriptor.Source, lane.descriptor.Path = lane.Name, lane.Name
				lane.Note = ""
				out = append(out, lane)
			}
		}
		return owner, out
	}
	for _, sampler := range score.Samplers {
		if sampler.Name == kind {
			owner, _ := catalog.Owner("sampler")
			return owner, nil // a sampler track takes only mixer settings
		}
	}
	name := kind
	if _, ok := modal.ParseTrackKind(kind); !ok && keyboard.ID(kind) == 0 {
		switch kind {
		case "acid", "drums", "piano", "guitar":
		default:
			return Owner{Name: kind, Kind: "unknown"}, nil
		}
	}
	owner, _ := catalog.Owner(name)
	return owner, catalog.Of(name)
}

func declares(inst notation.Instrument, name string) bool {
	for _, p := range inst.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sourceValue reads a source literal as the control's catalog value: a word,
// or a number in the control's unit.
func sourceValue(c Control, literal string) any {
	literal = strings.TrimSpace(literal)
	n, unit, err := paramdefs.ParseBase(literal)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return literal
	}
	if unit == "unit" && c.secondsPlain {
		n *= 1000 // a keyboard time written as a plain number is seconds
	}
	return n
}

// TrackControlNamed finds one control of a track by the name its track block
// writes, among the voice's controls (not the mixer strip's).
func TrackControlNamed(score *notation.Score, track, name string) (TrackControl, TrackView, bool, error) {
	view, err := ForTrack(score, track)
	if err != nil {
		return TrackControl{}, view, false, err
	}
	for _, c := range view.Controls {
		if c.Owner == "mix" {
			continue
		}
		if c.Name == name || contains(c.Aliases, name) {
			return c, view, true, nil
		}
	}
	return TrackControl{}, view, false, nil
}
