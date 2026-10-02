package project

import (
	"fmt"

	"m31labs.dev/cicada/kernel"
	"m31labs.dev/cicada/kernel/voice/guitar"
	"m31labs.dev/cicada/notation"
)

func CompileGuitarParams(track notation.Track) (guitar.Params, error) {
	values := make(map[string]Value, len(track.Params))
	for _, source := range track.Params {
		if isMixerSourceParam(source.Name) {
			continue
		}
		value, err := projectValue(source.Value)
		if err != nil {
			return guitar.Params{}, err
		}
		values[source.Name] = value
	}
	return guitarParamsFromValues(values)
}

func guitarParamsFromValues(values map[string]Value) (guitar.Params, error) {
	params := guitar.DefaultParams()
	optIn := values["experimental"]
	if !(optIn.Unit == "enum" && optIn.Text == "on" || optIn.Unit == "unit" && optIn.Number != nil && *optIn.Number == 1) {
		return params, fmt.Errorf("CICADA-EXPERIMENTAL: guitar requires experimental = on")
	}
	for _, name := range sortedKeys(values) {
		value := values[name]
		descriptor, ok := LookupParamDescriptor("guitar." + name)
		if !ok {
			return params, fmt.Errorf("CICADA-PARAM: unknown guitar parameter %s", name)
		}
		if err := validateParameterValue(descriptor, value); err != nil {
			code := "CICADA-PARAM"
			if value.Number == nil || value.Unit != "unit" {
				code = "CICADA-UNIT"
			}
			return params, fmt.Errorf("%s: guitar %s: %w", code, name, err)
		}
		if name == "experimental" {
			continue
		}
		if name == "octave" {
			if *value.Number != float64(int(*value.Number)) {
				return params, fmt.Errorf("CICADA-PARAM: guitar octave must be an integer")
			}
			continue
		}
		id, _ := kernel.FindParam(descriptor.ID)
		if err := params.Set(id, *value.Number); err != nil {
			return params, err
		}
	}
	return params, params.Validate()
}
