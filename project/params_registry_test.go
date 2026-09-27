package project

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

func TestParameterRegistryGeneratedFilesAreCurrent(t *testing.T) {
	wantJSON, err := paramdefs.GenerateJSON()
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := os.ReadFile("params.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatal("project/params.json is stale; run go run ./cmd/paramgen")
	}
	wantKernel, err := paramdefs.GenerateKernel()
	if err != nil {
		t.Fatal(err)
	}
	gotKernel, err := os.ReadFile("../kernel/params_table.go")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotKernel) != string(wantKernel) {
		t.Fatal("kernel/params_table.go is stale; run go run ./cmd/paramgen")
	}
	if len(kernel.Params) != len(paramdefs.Registry) {
		t.Fatalf("kernel table has %d entries, source list has %d", len(kernel.Params), len(paramdefs.Registry))
	}
	for i, descriptor := range paramdefs.Registry {
		got := kernel.Params[i]
		if got.ID != kernel.ParamID(i) || got.Name != descriptor.ID || got.Path != descriptor.Path || got.Min != float32(descriptor.Min) || got.Max != float32(descriptor.Max) || got.Default != float32(descriptor.Default) || got.DisplayStep != float32(descriptor.DisplayStep) || got.Curve != descriptor.Curve || got.SmoothingMS != float32(descriptor.SmoothingMS) || got.Live != descriptor.Live || got.Automatable != descriptor.Automatable {
			t.Fatalf("kernel registry row %d differs from source list: %+v vs %+v", i, got, descriptor)
		}
	}
}

func TestParameterRangesMatchProjectValidatorsAndSchema(t *testing.T) {
	fields, err := os.ReadFile("schema/cicada.fields-1.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Fields []struct {
			Construct string  `json:"construct"`
			Name      string  `json:"name"`
			Range     *string `json:"range"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(fields, &schema); err != nil {
		t.Fatal(err)
	}
	schemaRanges := make(map[string]string)
	for _, field := range schema.Fields {
		if field.Range != nil {
			schemaRanges[field.Construct+"."+field.Name] = *field.Range
		}
	}
	for _, descriptor := range paramdefs.Registry {
		if strings.HasPrefix(descriptor.ID, "global.") {
			continue // top-level source settings have no v1 track/effect compiler
		}
		if descriptor.Schema != "" {
			got, ok := schemaRanges[descriptor.Schema]
			want := strconv.FormatFloat(descriptor.Min, 'f', -1, 64) + ".." + strconv.FormatFloat(descriptor.Max, 'f', -1, 64)
			if !ok || got != want {
				t.Errorf("%s schema range is %q, want %q", descriptor.ID, got, want)
			}
		}
		if descriptor.Curve == "enum" || descriptor.Curve == "toggle" {
			continue
		}
		if !projectValidatorAccepts(descriptor, descriptor.Min) || !projectValidatorAccepts(descriptor, descriptor.Max) {
			t.Errorf("project validator rejects an endpoint for %s [%g,%g]", descriptor.ID, descriptor.Min, descriptor.Max)
		}
		span := descriptor.Max - descriptor.Min
		delta := math.Max(1e-6, span*1e-5)
		if projectValidatorAccepts(descriptor, descriptor.Min-delta) || projectValidatorAccepts(descriptor, descriptor.Max+delta) {
			t.Errorf("project validator accepts outside the registry range for %s [%g,%g]", descriptor.ID, descriptor.Min, descriptor.Max)
		}
	}
}

func projectValidatorAccepts(descriptor paramdefs.Descriptor, number float64) bool {
	if strings.HasPrefix(descriptor.ID, "mix.") {
		name := descriptor.Source
		suffix := ""
		if descriptor.Unit == "dB" {
			suffix = "db"
		}
		parameter := notation.Param{Name: name, Value: strconv.FormatFloat(number, 'f', -1, 64) + suffix}
		_, err := CompileMixerParams(notation.Track{Params: []notation.Param{parameter}})
		return err == nil
	}
	if strings.HasPrefix(descriptor.ID, "acid.") {
		value := strconv.FormatFloat(number, 'f', -1, 64)
		if descriptor.Unit == "Hz" {
			value += "hz"
		} else if descriptor.Unit == "ms" {
			value += "ms"
		}
		_, err := CompileAcidParams(notation.Track{Params: []notation.Param{{Name: descriptor.Source, Value: value}}})
		return err == nil
	}
	if strings.HasPrefix(descriptor.ID, "drum.") {
		value := strconv.FormatFloat(number, 'f', -1, 64)
		if descriptor.Unit == "dB" {
			value += "db"
		} else if descriptor.Unit == "Hz" {
			value += "hz"
		} else if descriptor.Unit == "ms" {
			value += "ms"
		}
		_, err := CompileDrumParams(notation.Track{Params: []notation.Param{{Name: descriptor.Source, Value: value}}})
		return err == nil
	}
	numberValue := number
	unit := strings.ToLower(descriptor.Unit)
	if unit == "ratio" || unit == "" {
		unit = "unit"
	}
	value := Value{Unit: unit, Number: &numberValue}
	values := map[string]Value{descriptor.Source: value}
	switch {
	case strings.HasPrefix(descriptor.ID, "fx.drive."):
		_, err := DriveParamsFromValues(values)
		return err == nil
	case strings.HasPrefix(descriptor.ID, "fx.delay."):
		_, err := DelayParamsFromValues(values)
		return err == nil
	case strings.HasPrefix(descriptor.ID, "fx.reverb."):
		_, err := ReverbParamsFromValues(values)
		return err == nil
	case strings.HasPrefix(descriptor.ID, "fx.comp."):
		_, _, err := CompSpecFromValues(values)
		return err == nil
	default:
		return false
	}
}

func TestParameterRegistryIncludesAllBuiltInDrumLanes(t *testing.T) {
	for lane := drum.Lane(0); lane < drum.LaneCount; lane++ {
		for _, field := range []string{"level", "pan"} {
			if _, ok := LookupParamDescriptor("drum." + drum.Names[lane] + "." + field); !ok {
				t.Errorf("registry misses %s_%s", drum.Names[lane], field)
			}
		}
	}
}
