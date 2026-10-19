package render

import (
	"bytes"
	"path/filepath"
	"testing"

	"m31labs.dev/cicada/kernel/seq"
	"m31labs.dev/cicada/notation"
)

func TestPerformedWAVKeepsGatesAcrossBlocksAndStems(t *testing.T) {
	score, ds := notation.Parse([]byte(`tempo 127.125
track keys piano { level=-12dB }
pattern phrase performance length=3840ticks {
 note 60 at=17ticks length=703ticks velocity=82;
 note 64 at=19ticks length=1201ticks velocity=105;
 note 67 at=491ticks length=513ticks velocity=47;
}
scene main { keys=phrase }
song { main*2 }
`))
	if score == nil {
		t.Fatal(ds)
	}
	if !UsesScheduledEngine(score, Options{}) {
		t.Fatal("performance fell through the monophonic legacy mixer")
	}
	var reference []byte
	for _, block := range []int{1, 127, 1024} {
		var wav bytes.Buffer
		var events []seq.Event
		_, err := WAV(score, Options{Bits: 32, Block: block, TailSec: .02, NoteTap: func(_ int, event seq.Event) { events = append(events, event) }}, &wav)
		if err != nil {
			t.Fatal(err)
		}
		if reference == nil {
			reference = bytes.Clone(wav.Bytes())
		} else if !bytes.Equal(reference, wav.Bytes()) {
			t.Fatalf("performed WAV depends on block %d", block)
		}
		for iteration := int64(0); iteration < 2; iteration++ {
			for _, gate := range []struct {
				note     uint8
				at, end  int64
				velocity uint8
			}{{60, 17, 720, 82}, {64, 19, 1220, 105}, {67, 491, 1004, 47}} {
				on, off := false, false
				for _, event := range events {
					if event.Note != gate.note {
						continue
					}
					on = on || event.Kind == seq.NoteOn && event.Tick == iteration*3840+gate.at && event.Velocity == gate.velocity
					off = off || event.Kind == seq.NoteOff && event.Tick == iteration*3840+gate.end
				}
				if !on || !off {
					t.Fatalf("lost performed gate in WAV: iteration=%d gate=%+v", iteration, gate)
				}
			}
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
