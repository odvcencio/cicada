package audition

import (
	"bytes"
	"m31labs.dev/cicada/audioasset"
	"testing"
)

type delayed struct {
	l, r [3]float32
	at   int
}

func (d *delayed) Process(l, r float32) (float32, float32) {
	a, b := d.l[d.at], d.r[d.at]
	d.l[d.at], d.r[d.at] = l, r
	d.at = (d.at + 1) % 3
	return a, b
}
func (*delayed) LatencyFrames() int { return 3 }
func (*delayed) Fault() bool        { return false }
func TestAuditionCompensatesFinalLatencyAndWritesFloatPCM(t *testing.T) {
	a := Audio{Rate: 48000, Left: []float32{1, 2, 3, 4, 5}, Right: []float32{5, 4, 3, 2, 1}}
	if err := a.Process(&delayed{}); err != nil {
		t.Fatal(err)
	}
	for i := range a.Left {
		if a.Left[i] != float32(i+1) || a.Right[i] != float32(5-i) {
			t.Fatal("lost delayed final frames")
		}
	}
	var wav bytes.Buffer
	if err := a.WriteWAV(&wav); err != nil {
		t.Fatal(err)
	}
	h, err := audioasset.ReadWAVHeader(bytes.NewReader(wav.Bytes()))
	if err != nil || h.Encoding != "float" || h.Frames != 5 || h.Channels != 2 {
		t.Fatalf("invalid output %+v %v", h, err)
	}
}
