package browsercontrol

import (
	"m31labs.dev/cicada/kernel/cmd"
	"math"
	"testing"
)

func TestParameterFloat32OverflowCannotBecomeOff(t *testing.T) {
	c, records := recordingController(t)
	for _, value := range []float64{-math.MaxFloat64, math.MaxFloat64} {
		before := len(*records)
		if err := c.SetParam("bass.level", &value); err == nil || len(*records) != before {
			t.Errorf("finite float64 overflow reached sender: value=%g err=%v commands=%d", value, err, len(*records)-before)
		}
	}
	before := len(*records)
	if err := c.SetParam("bass.level", nil); err != nil {
		t.Fatal(err)
	}
	if len(*records) != before+1 || (*records)[before].Op != cmd.OpSetParam || !math.IsInf(float64(math.Float32frombits((*records)[before].Arg0)), -1) {
		t.Fatal("explicit nil off no longer encodes the supported off sentinel")
	}
	before = len(*records)
	if err := c.SetParam("bass.cutoff", nil); err == nil || len(*records) != before {
		t.Fatal("off reached parameter that does not support it")
	}
}
