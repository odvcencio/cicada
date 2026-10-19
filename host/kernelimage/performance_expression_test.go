package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/seq"
)

func TestPerformedGesturesUseAdditiveVersion17AndExactValues(t *testing.T) {
	cfg := firstAcidConfig(t)
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 55, Performance: &seq.Performance{LengthTicks: 3840, Notes: []seq.PerformedNote{{Tick: 17, EndTick: 720, Note: 60, Velocity: 82}}}}
	legacy, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Patterns[0].Slots[0].Performance.Notes[0].Expressions = []seq.PerformedExpression{
		{Tick: 0, Params: seq.Expression{Set: true, Timbre: .5, Pressure: .12345679}},
		{Tick: 123, Params: seq.Expression{Set: true, PitchCents: 123.125, Pressure: .7, Timbre: .9876543, VibratoRateHz: 5, VibratoDepthCents: 30}},
	}
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 17 || kernelimage.CapabilityGestures != 1<<19 {
		t.Fatal("gesture capability or version changed")
	}
	prefix := append([]byte(nil), data[:len(legacy)]...)
	binary.LittleEndian.PutUint16(prefix[4:6], 16)
	if !bytes.Equal(prefix, legacy) {
		t.Fatal("gesture table changed the version-16 prefix")
	}
	restored, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, restored) {
		t.Fatalf("gesture precision or metadata changed: %v", err)
	}
	for _, corrupt := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint16(b[4:6], 16) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[len(legacy):], 0) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[len(legacy)+8:], 65537) },
		func(b []byte) { binary.LittleEndian.PutUint64(b[len(legacy)+12:], 704) },
	} {
		bad := append([]byte(nil), data...)
		corrupt(bad)
		if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
			t.Fatal("corrupt gesture payload admitted")
		}
	}
}
