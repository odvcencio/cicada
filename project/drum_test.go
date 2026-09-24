package project

import (
	"testing"

	"m31labs.dev/cicada/kernel/voice/drum"
	"m31labs.dev/cicada/notation"
)

func TestCompileDrumParamsUnitsAndBounds(t *testing.T) {
	track := notation.Track{Kind: "drums", Params: []notation.Param{
		{Name: "bd_tune", Value: "72hz"},
		{Name: "bd_decay", Value: "520ms"},
		{Name: "ch_metal", Value: "on"},
	}}
	params, err := CompileDrumParams(track)
	if err != nil {
		t.Fatal(err)
	}
	if params[drum.BD].Tune != 72 || params[drum.BD].Decay != .52 || !params[drum.CH].Metal {
		t.Fatalf("unexpected drum params: %+v", params)
	}
	for _, source := range []notation.Param{
		{Name: "bd_tune", Value: "72ms"},
		{Name: "bd_decay", Value: "3000ms"},
		{Name: "lt_tune", Value: "1"},
		{Name: "ch_sweep", Value: "2"},
		{Name: "bd_metal", Value: "on"},
	} {
		_, err := CompileDrumParams(notation.Track{Kind: "drums", Params: []notation.Param{source}})
		if err == nil {
			t.Fatalf("accepted %s=%s", source.Name, source.Value)
		}
	}
}
