package render

import (
	"bytes"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestPerformedGestureWAVIsBlockInvariantAndStemsNull(t *testing.T) {
	score, ds := notation.Parse([]byte(`tempo 127.125
instrument glass { voice poly { out=sine(pitch)*env(gate,300ms)*velocity*(0.1+pressure*0.1+timbre*0.1) } }
track keys glass {}
pattern phrase performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=82 {
  gesture at=0ticks bend=0ct pressure=0.2 timbre=0.7;
  gesture at=123ticks bend=127.5ct pressure=0.8 timbre=0.1 vibrato=14ct rate=7.25Hz;
  gesture at=500ticks bend=-50ct pressure=0.4 timbre=0.9;
 }
 note 64 at=19ticks length=1201ticks velocity=105 {
  gesture at=0ticks bend=-100ct pressure=0.4 timbre=0.5;
  gesture at=391ticks bend=200ct pressure=0.1 timbre=0.8 vibrato=10ct rate=3Hz;
 }
}
scene main { keys=phrase }
song { main*2 }
`))
	if score == nil || len(ds) > 0 {
		t.Fatal(ds)
	}
	var reference []byte
	for _, block := range []int{1, 127, 4096} {
		var wav bytes.Buffer
		if _, err := WAV(score, Options{Bits: 32, Block: block, TailSec: .02}, &wav); err != nil {
			t.Fatal(err)
		}
		if reference == nil {
			reference = bytes.Clone(wav.Bytes())
		} else if !bytes.Equal(reference, wav.Bytes()) {
			t.Fatal("gesture WAV changed with block size", block)
		}
	}
	dir := filepath.Join(t.TempDir(), "stems")
	if _, err := Stems(score, Options{Bits: 32, Block: 127, TailSec: .02}, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyStems(nil, dir, VerifyStemsOptions{ResidualMaxDB: -80}); err != nil {
		t.Fatal(err)
	}
}
