// Package paramdefs is the single source list for the live parameter registry.
package paramdefs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"strings"
)

type Descriptor struct {
	ID          string   `json:"id"`
	Scope       string   `json:"scope"`
	Voices      []string `json:"voices"`
	Source      string   `json:"source"`
	Unit        string   `json:"unit"`
	Min         float64  `json:"min"`
	Max         float64  `json:"max"`
	Default     float64  `json:"default"`
	Curve       string   `json:"curve"`
	Values      []string `json:"values,omitempty"`
	Off         bool     `json:"off,omitempty"`
	SmoothingMS float64  `json:"smoothing_ms"`
	Live        bool     `json:"live"`
	Persist     string   `json:"persist"`
	Schema      string   `json:"-"`
}

type drumRange struct {
	name, unit, curve string
	min, max, def     float64
}

type drumLane struct {
	name   string
	params []drumRange
}

func parameter(id, scope, source, unit string, min, max, def float64, curve string, live bool, smoothing float64, persist string, voices ...string) Descriptor {
	return Descriptor{ID: id, Scope: scope, Voices: voices, Source: source, Unit: unit, Min: min, Max: max, Default: def, Curve: curve, Live: live, SmoothingMS: smoothing, Persist: persist}
}

// Registry is kept in stable declaration order because ParamID is an ABI.
var Registry = buildRegistry()

func buildRegistry() []Descriptor {
	list := []Descriptor{
		{ID: "mix.gain", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "level", Unit: "dB", Min: -60, Max: 6, Default: -6, Curve: "fader", Off: true, SmoothingMS: 5, Live: true, Persist: "source", Schema: "mixer.gain_db"},
		{ID: "mix.pan", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "pan", Min: -1, Max: 1, Default: 0, Curve: "linear", SmoothingMS: 5, Live: true, Persist: "source", Schema: "mixer.pan"},
		{ID: "mix.send_a", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "send_a", Min: 0, Max: 1, Default: 0, Curve: "linear", SmoothingMS: 5, Live: true, Persist: "source", Schema: "mixer.send_a"},
		{ID: "mix.send_b", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "send_b", Min: 0, Max: 1, Default: 0, Curve: "linear", SmoothingMS: 5, Live: true, Persist: "source", Schema: "mixer.send_b"},
		{ID: "mix.mute", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "mute", Min: 0, Max: 1, Default: 0, Curve: "toggle", SmoothingMS: 10, Live: true, Persist: "live-only"},
		{ID: "mix.solo", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "solo", Min: 0, Max: 1, Default: 0, Curve: "toggle", SmoothingMS: 10, Live: true, Persist: "live-only"},
		{ID: "mix.send_pre", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "send_pre", Min: 0, Max: 1, Default: 0, Curve: "toggle", Values: []string{"false", "true"}, SmoothingMS: 0, Live: false, Persist: "source"},
		{ID: "mix.bus", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "bus", Min: 0, Max: 1, Default: 0, Curve: "enum", Values: []string{"music", "sfx"}, SmoothingMS: 0, Live: false, Persist: "source"},
		{ID: "mix.insert", Scope: "track", Voices: []string{"acid", "drums", "instrument"}, Source: "insert", Min: 0, Max: 1, Default: 0, Curve: "enum", Values: []string{"none", "drive"}, SmoothingMS: 0, Live: false, Persist: "source"},

		{ID: "fx.drive.shape", Scope: "global", Voices: []string{}, Source: "shape", Min: 0, Max: 3, Default: 0, Curve: "enum", Values: []string{"soft", "hard", "fold", "diode"}, SmoothingMS: 0, Live: false, Persist: "source"},
		parameter("fx.drive.gain", "global", "gain", "dB", 0, 36, 0, "linear", true, 5, "source"),
		parameter("fx.drive.tone", "global", "tone", "Hz", 1000, 20000, 12000, "log", true, 5, "source"),
		parameter("fx.drive.mix", "global", "mix", "ratio", 0, 1, 1, "linear", true, 5, "source"),

		{ID: "fx.delay.time", Scope: "global", Voices: []string{}, Source: "time", Unit: "ms", Min: 1, Max: 2000, Default: 250, Curve: "log", Values: []string{"1/32", "1/16", "1/16T", "1/16.", "1/8", "1/8T", "1/8.", "3/16", "1/4", "1/4.", "1/2"}, SmoothingMS: 20, Live: true, Persist: "source"},
		parameter("fx.delay.feedback", "global", "feedback", "ratio", 0, .95, .35, "linear", true, 5, "source"),
		parameter("fx.delay.damp", "global", "damp", "Hz", 1000, 16000, 6000, "log", true, 5, "source"),
		{ID: "fx.delay.pingpong", Scope: "global", Voices: []string{}, Source: "pingpong", Min: 0, Max: 1, Default: 0, Curve: "toggle", Values: []string{"false", "true"}, SmoothingMS: 5, Live: true, Persist: "source"},
		parameter("fx.delay.width", "global", "width", "ratio", 0, 1, 1, "linear", true, 5, "source"),
		parameter("fx.delay.mix", "global", "mix", "ratio", 0, 1, 1, "linear", true, 5, "source"),

		parameter("fx.reverb.size", "global", "size", "ratio", .5, 1.5, 1, "linear", true, 5, "source"),
		parameter("fx.reverb.decay", "global", "decay", "ms", 300, 12000, 2400, "log", true, 5, "source"),
		parameter("fx.reverb.damp", "global", "damp", "Hz", 2000, 16000, 8000, "log", true, 5, "source"),
		parameter("fx.reverb.highpass", "global", "highpass", "Hz", 40, 400, 120, "log", true, 5, "source"),
		parameter("fx.reverb.predelay", "global", "predelay", "ms", 0, 200, 0, "linear", true, 5, "source"),
		parameter("fx.reverb.mix", "global", "mix", "ratio", 0, 1, 1, "linear", true, 5, "source"),

		{ID: "fx.comp.detect", Scope: "global", Voices: []string{}, Source: "detect", Min: 0, Max: 1, Default: 0, Curve: "enum", Values: []string{"peak", "rms"}, SmoothingMS: 0, Live: false, Persist: "source"},
		parameter("fx.comp.threshold", "global", "threshold", "dB", -40, 0, -18, "linear", true, 5, "source"),
		parameter("fx.comp.ratio", "global", "ratio", "ratio", 1, 20, 4, "linear", true, 5, "source"),
		parameter("fx.comp.knee", "global", "knee", "dB", 0, 12, 6, "linear", true, 5, "source"),
		parameter("fx.comp.attack", "global", "attack", "ms", .1, 100, 10, "log", true, 5, "source"),
		parameter("fx.comp.release", "global", "release", "ms", 10, 1000, 100, "log", true, 5, "source"),
		{ID: "fx.comp.makeup", Scope: "global", Voices: []string{}, Source: "makeup", Unit: "dB", Min: -24, Max: 24, Default: 0, Curve: "linear", Values: []string{"auto"}, SmoothingMS: 5, Live: true, Persist: "source"},
		parameter("fx.comp.mix", "global", "mix", "ratio", 0, 1, 1, "linear", true, 5, "source"),
		{ID: "fx.comp.sidechain", Scope: "global", Voices: []string{}, Source: "sidechain", Min: 0, Max: 1, Default: 0, Curve: "enum", Values: []string{"music", "sfx"}, SmoothingMS: 0, Live: false, Persist: "source"},

		parameter("acid.tune", "track", "tune", "", -12, 12, 0, "linear", false, 5, "source", "acid"),
		parameter("acid.fine", "track", "fine", "", -100, 100, 0, "linear", false, 5, "source", "acid"),
		parameter("acid.wave", "track", "wave", "", 0, 1, 0, "linear", false, 5, "source", "acid"),
		parameter("acid.pw", "track", "pw", "", .1, .9, .5, "linear", false, 5, "source", "acid"),
		parameter("acid.detune", "track", "detune", "", 0, 50, 0, "linear", false, 5, "source", "acid"),
		parameter("acid.sub", "track", "sub", "", 0, 1, 0, "linear", false, 5, "source", "acid"),
		parameter("acid.cutoff", "track", "cutoff", "Hz", 20, 8000, 600, "log", false, 3, "source", "acid"),
		parameter("acid.reso", "track", "reso", "", 0, 1, .55, "linear", false, 5, "source", "acid"),
		parameter("acid.envmod", "track", "envmod", "", 0, 1, .6, "linear", false, 5, "source", "acid"),
		parameter("acid.decay", "track", "decay", "ms", 100, 3000, 400, "log", false, 5, "source", "acid"),
		parameter("acid.accent", "track", "accent", "", 0, 1, .7, "linear", false, 5, "source", "acid"),
		parameter("acid.drive", "track", "drive", "", 0, 1, .2, "linear", false, 5, "source", "acid"),
		parameter("acid.release", "track", "release", "ms", 5, 500, 30, "log", false, 5, "source", "acid"),
		parameter("acid.slide", "track", "slide", "ms", 20, 200, 60, "log", false, 5, "source", "acid"),
		parameter("acid.gate", "track", "gate", "", 10, 100, 55, "linear", false, 5, "source", "acid"),
		{ID: "acid.filter", Scope: "track", Voices: []string{"acid"}, Source: "filter", Min: 0, Max: 1, Default: 0, Curve: "enum", Values: []string{"diode", "ladder"}, SmoothingMS: 0, Live: false, Persist: "source"},
		{ID: "acid.savage", Scope: "track", Voices: []string{"acid"}, Source: "savage", Min: 0, Max: 1, Default: 0, Curve: "toggle", Values: []string{"off", "on"}, SmoothingMS: 0, Live: false, Persist: "source"},
		{ID: "acid.octave", Scope: "track", Voices: []string{"acid"}, Source: "octave", Min: 0, Max: 6, Default: 2, Curve: "enum", Values: []string{"0", "1", "2", "3", "4", "5", "6"}, SmoothingMS: 0, Live: false, Persist: "source"},
	}

	lanes := []drumLane{
		{name: "bd", params: []drumRange{{"tune", "Hz", "log", 40, 120, 55}, {"decay", "ms", "log", 80, 1500, 400}, {"sweep", "", "linear", 1, 8, 4}, {"sweep_time", "ms", "log", 10, 80, 30}, {"click", "ratio", "linear", 0, 1, .3}, {"drive", "ratio", "linear", 0, 1, .2}}},
		{name: "sd", params: []drumRange{{"tune", "ratio", "linear", .7, 1.4, 1}, {"tone", "ratio", "linear", .5, 2, 1}, {"mix", "ratio", "linear", 0, 1, .6}, {"snappy", "ms", "log", 60, 400, 180}, {"decay", "ms", "log", 40, 300, 90}}},
		{name: "ch", params: []drumRange{{"tune", "ratio", "linear", .8, 1.25, 1}, {"tone", "Hz", "log", 5000, 10000, 7500}, {"decay", "ms", "log", 20, 150, 60}, {"metal", "", "toggle", 0, 1, 0}}},
		{name: "oh", params: []drumRange{{"tune", "ratio", "linear", .8, 1.25, 1}, {"tone", "Hz", "log", 5000, 10000, 7500}, {"decay", "ms", "log", 150, 1200, 400}, {"metal", "", "toggle", 0, 1, 0}}},
		{name: "cp", params: []drumRange{{"tone", "Hz", "log", 700, 2000, 1100}, {"decay", "ms", "log", 60, 400, 120}, {"spread", "ms", "log", 6, 16, 10}}},
		{name: "rs", params: []drumRange{{"tune", "ratio", "linear", .7, 1.4, 1}, {"decay", "ms", "log", 4, 50, 12}}},
		{name: "lt", params: []drumRange{{"tune", "ratio", "linear", .6674199270850172, 1.4983070768766815, 1}, {"decay", "ms", "log", 80, 1000, 250}, {"sweep", "ratio", "linear", 0, 2, .4}}},
		{name: "mt", params: []drumRange{{"tune", "ratio", "linear", .6674199270850172, 1.4983070768766815, 1}, {"decay", "ms", "log", 80, 1000, 250}, {"sweep", "ratio", "linear", 0, 2, .4}}},
		{name: "ht", params: []drumRange{{"tune", "ratio", "linear", .6674199270850172, 1.4983070768766815, 1}, {"decay", "ms", "log", 80, 1000, 250}, {"sweep", "ratio", "linear", 0, 2, .4}}},
		{name: "cb", params: []drumRange{{"tune", "ratio", "linear", .7, 1.4, 1}, {"decay", "ms", "log", 40, 400, 120}}},
		{name: "cy", params: []drumRange{{"tune", "ratio", "linear", .8, 1.25, 1}, {"decay", "ms", "log", 500, 4000, 1500}, {"tone", "ratio", "linear", .5, 2, 1}}},
	}
	for _, lane := range lanes {
		for _, p := range lane.params {
			id, source := "drum."+lane.name+"."+p.name, lane.name+"_"+p.name
			d := parameter(id, "track", source, p.unit, p.min, p.max, p.def, p.curve, false, 5, "source", "drums")
			if p.curve == "toggle" {
				d.Values = []string{"off", "on"}
			}
			list = append(list, d)
		}
		level := parameter("drum."+lane.name+".level", "track", lane.name+"_level", "dB", -60, 6, -6, "fader", false, 5, "source", "drums")
		level.Off = true
		level.Schema = ""
		list = append(list, level, parameter("drum."+lane.name+".pan", "track", lane.name+"_pan", "", -1, 1, 0, "linear", false, 5, "source", "drums"))
	}
	return list
}

func GoName(id string) string {
	name := strings.NewReplacer(".", "_", "-", "_").Replace(id)
	parts := strings.Split(name, "_")
	for i := range parts {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

func GenerateJSON() ([]byte, error) {
	data, err := json.MarshalIndent(Registry, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func GenerateKernel() ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by cmd/paramgen; DO NOT EDIT.")
	fmt.Fprintln(&b, "package kernel")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "type ParamID uint16")
	fmt.Fprintln(&b, "type ParamSpec struct { ID ParamID; Name, Scope, Source, Unit, Curve, Persist string; Min, Max, Default float32; SmoothingMS float32; Live, Off bool }")
	fmt.Fprintln(&b, "const (")
	for i, d := range Registry {
		fmt.Fprintf(&b, "\tParam%s ParamID = %d\n", GoName(d.ID), i)
	}
	fmt.Fprintf(&b, "\tParamCount = %d\n", len(Registry))
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b, "var Params = [...]ParamSpec{")
	for i, d := range Registry {
		fmt.Fprintf(&b, "\t{%d, %q, %q, %q, %q, %q, %q, %s, %s, %s, %s, %t, %t},\n", i, d.ID, d.Scope, d.Source, d.Unit, d.Curve, d.Persist, floatLiteral(d.Min), floatLiteral(d.Max), floatLiteral(d.Default), floatLiteral(d.SmoothingMS), d.Live, d.Off)
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b, "func Param(id ParamID) (ParamSpec, bool) { if int(id) >= len(Params) { return ParamSpec{}, false }; return Params[id], true }")
	fmt.Fprintln(&b, "func FindParam(name string) (ParamID, bool) { for i := range Params { if Params[i].Name == name { return ParamID(i), true } }; return 0, false }")
	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated parameter table: %w", err)
	}
	return formatted, nil
}

func floatLiteral(value float64) string { return fmt.Sprintf("%g", value) }
