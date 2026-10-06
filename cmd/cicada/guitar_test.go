package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/project"
)

func TestGuitarStudioRegistryAndExplain(t *testing.T) {
	source, err := os.ReadFile("../../examples/expressive-guitar.cicada")
	if err != nil {
		t.Fatal(err)
	}
	handler, path := studioTestHandler(t)
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	response := studioCall(t, handler, "/api/params", nil)
	if response.Code != 200 {
		t.Fatalf("Studio parameters: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Registry  []paramdefs.Descriptor `json:"registry"`
		Addresses []project.ParamAddress `json:"addresses"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bend", "vibrato", "brightness", "damping", "pickup", "drive"} {
		found := false
		for _, d := range result.Registry {
			if d.ID == "guitar."+name {
				found = true
				if d.Type != "number" || d.Unit == "" || d.SmoothingMS != 8 || !d.Live {
					t.Fatalf("Studio metadata: %+v", d)
				}
			}
		}
		if !found {
			t.Fatalf("Studio lost guitar.%s", name)
		}
		found = false
		for _, address := range result.Addresses {
			found = found || address.Address == "lead."+name && address.Param == "guitar."+name
		}
		if !found {
			t.Fatalf("Studio lost lead.%s", name)
		}
	}
	var output strings.Builder
	if err := explainParameter(path, "lead.vibrato", "@2", &output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type: number; unit: cent; range: 0..100; smoothing: 8 ms", "scene bend (entered bar 2): 25", "computed: 25"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("explain missing %s: %s", want, output.String())
		}
	}
}
