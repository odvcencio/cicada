//go:build !js || !wasm

package padsampler

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/padsampler/protocol"
)

func TestStoredBankAndBrowserProtocolStayCompatible(t *testing.T) {
	// Captured from the previous Studio shell regression, before separating
	// the browser protocol from host types. It includes two synthetic sources
	// and a performance that refers to an immutable prepared sound.
	data, err := os.ReadFile("testdata/bank-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var host Bank
	if err := json.Unmarshal(data, &host); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(BankBytes(host), data) {
		t.Fatal("host serialization changed the existing bank bytes")
	}
	var browser protocol.Bank[protocol.AudioInfo]
	if err := json.Unmarshal(data, &browser); err != nil {
		t.Fatal(err)
	}
	if browser.Version != host.Version || browser.Pads != host.Pads || browser.Effects != host.Effects || !reflect.DeepEqual(browser.Performance, host.Performance) {
		t.Fatal("browser lost bank settings or recorded performance")
	}
	if len(host.Sources) != 2 || len(browser.Sources) != len(host.Sources) || len(browser.Performance.Hits) == 0 {
		t.Fatal("fixture lost its sources or performance")
	}
	for _, source := range host.Sources {
		view, ok := browser.Source(source.ID)
		if !ok || view.Name != source.Name || view.Asset.Frames != source.Asset.Frames || view.Asset.RateHz != source.Asset.RateHz || !reflect.DeepEqual(view.Peaks, source.Peaks) {
			t.Fatalf("browser lost waveform metadata for %s", source.ID)
		}
		if err := browser.Pads[0].Validate(view.Asset.Frames); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := browser.Source("missing"); ok {
		t.Fatal("missing source matched")
	}
	// The browser submits the performance, never the reduced asset metadata.
	posted, err := json.Marshal(browser.Performance)
	if err != nil {
		t.Fatal(err)
	}
	var received Performance
	if err := json.Unmarshal(posted, &received); err != nil || !reflect.DeepEqual(received, host.Performance) {
		t.Fatalf("browser performance changed recorded sound references: %v", err)
	}
}
