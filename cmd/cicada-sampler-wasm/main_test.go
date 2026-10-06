package main

import (
	"math"
	"testing"
)

func TestSamplerAdmissionOwnershipAndSilence(t *testing.T) {
	if initialize(48000, 1) != 0 || initialize(48000, 1) == 0 {
		t.Fatal("instance admission")
	}
	if allocate(1<<30, 2, 48000) >= 0 {
		t.Fatal("PCM budget")
	}
	index := allocate(512, 1, 48000)
	if index < 0 {
		t.Fatal("allocate")
	}
	for i := range assets[index].Left {
		assets[index].Left[i] = .25
	}
	setup = [18]float64{2, 2, 0, 1, 2, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 4242, 0}
	descriptors[0] = [20]float64{float64(index), 60, 60, 60, 1, 127, 64, 0, 0, 1, 0, 1, 0, 0, 512, 1, 0, 512, 0, 0}
	assets[0].Left[0] = float32(math.NaN())
	if prepare() == 0 {
		t.Fatal("NaN PCM admitted")
	}
	assets[0].Left[0] = .25
	if prepare() != 0 || prepare() == 0 || allocate(1, 1, 48000) >= 0 {
		t.Fatal("sealing")
	}
	first := noteOn(60, 127)
	second := noteOn(60, 127)
	third := noteOn(60, 127)
	if first == 0 || second == 0 || third == 0 || noteOff(first) == 0 || noteOff(third) != 0 {
		t.Fatal("ownership")
	}
	if render(128) != 0 || render(4097) == 0 {
		t.Fatal("render bounds")
	}
	reset()
	render(128)
	for _, x := range output {
		if x != 0 {
			t.Fatal("reset silence")
		}
	}
}
