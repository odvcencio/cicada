package kernelimage_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"m31labs.dev/cicada/host/kernelimage"
	"m31labs.dev/cicada/kernel/expression"
	"m31labs.dev/cicada/kernel/seq"
)

func TestSharedGesturesUseVersion18AndPreserveLegacyPrefix(t *testing.T) {
	cfg := firstAcidConfig(t)
	p := &seq.Performance{LengthTicks: 3840, Notes: []seq.PerformedNote{{Note: 60, Velocity: 100, Tick: 17, EndTick: 720, Expressions: []seq.PerformedExpression{{Params: seq.Expression{Set: true, PitchCents: 25.125, Timbre: .7, Pressure: .3}}}}}}
	cfg.Patterns[0].Slots[0] = seq.Pattern{Len: 1, GatePercent: 55, Performance: p}
	legacy, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(legacy[4:6]) != 17 {
		t.Fatal("missing legacy gesture prefix")
	}
	p.Groups = []seq.ControlGroup{{Name: "touch", Points: []seq.GroupPoint{{Value: expression.Controls{Timbre: .5}}, {Tick: 960, Value: expression.Controls{PitchCents: 200, Pressure: .8, Timbre: .9}}}}}
	p.Notes[0].Group = 1
	data, err := kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != 18 || kernelimage.CapabilitySharedControls != 1<<21 {
		t.Fatal("shared image version or capability changed")
	}
	prefix := append([]byte(nil), data[:len(legacy)]...)
	binary.LittleEndian.PutUint16(prefix[4:6], 17)
	if !bytes.Equal(prefix, legacy) {
		t.Fatal("shared lanes changed the complete version-17 prefix")
	}
	decoded, err := kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("shared lane precision or identity changed", err)
	}
	firstPoint := len(legacy) + 2 + 1 + 1 + 2 + 1 + len("touch") + 4
	for _, corrupt := range []func([]byte){
		func(b []byte) { binary.LittleEndian.PutUint16(b[4:6], 17) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[len(legacy):], 65535) },
		func(b []byte) { b[len(legacy)+6] = 65 },
		func(b []byte) { binary.LittleEndian.PutUint64(b[firstPoint:], 3840) },
		func(b []byte) { binary.LittleEndian.PutUint32(b[firstPoint+8:], math.Float32bits(float32(math.NaN()))) },
		func(b []byte) { binary.LittleEndian.PutUint16(b[len(b)-2:], 2) },
	} {
		bad := append([]byte(nil), data...)
		corrupt(bad)
		if _, err := kernelimage.Decode(bad, 48000, 128); err == nil {
			t.Fatal("corrupt shared lane was accepted")
		}
	}
	p.Notes[0].Expressions = nil
	data, err = kernelimage.Encode(cfg)
	if err != nil {
		t.Fatal("shared-only lane needed a dummy member gesture", err)
	}
	decoded, err = kernelimage.Decode(data, 48000, 128)
	if err != nil || !reflect.DeepEqual(cfg, decoded) {
		t.Fatal("shared-only lane did not roundtrip", err)
	}
}
