//go:build !js || !wasm

package padsampler

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"m31labs.dev/cicada/project"
)

func TestBrowserAssetMetadataWireContract(t *testing.T) {
	host, portable := reflect.TypeOf(project.Asset{}), reflect.TypeOf(assetMetadata{})
	count := 0
	for i := 0; i < host.NumField(); i++ {
		field := host.Field(i)
		if !field.IsExported() {
			continue
		}
		count++
		browser, ok := portable.FieldByName(field.Name)
		if !ok || browser.Type != field.Type || browser.Tag.Get("json") != field.Tag.Get("json") {
			t.Fatalf("browser asset metadata differs at %s", field.Name)
		}
	}
	if portable.NumField() != count {
		t.Fatal("browser metadata has fields not present on the host")
	}
	for _, provenance := range []string{"", "recorded"} {
		original := project.Asset{Name: "voice", Path: "audio/take.wav", SHA256: "abc", Format: "wav", Frames: 9007199254740991, RateHz: 48000, Channels: 2, Source: provenance}
		data, err := json.Marshal(Source{ID: "id", Asset: original})
		if err != nil {
			t.Fatal(err)
		}
		var browser struct {
			ID    string        `json:"id"`
			Name  string        `json:"name"`
			Asset assetMetadata `json:"asset"`
			Peaks []float32     `json:"peaks"`
		}
		if err = json.Unmarshal(data, &browser); err != nil {
			t.Fatal(err)
		}
		roundtrip, err := json.Marshal(browser)
		if err != nil || !bytes.Equal(data, roundtrip) {
			t.Fatalf("wire round trip changed: %s / %s / %v", data, roundtrip, err)
		}
	}
}
