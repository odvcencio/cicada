package recording

import (
	"encoding/binary"
	"testing"

	"m31labs.dev/cicada/internal/testwav"
)

// wav16 builds a 16-bit PCM WAV from frames of per-channel samples.
func wav16(frames [][]int16) []byte {
	channels := len(frames[0])
	data := testwav.Bytes(48000, channels, 16, len(frames), 1)
	for i, frame := range frames {
		for ch, v := range frame {
			binary.LittleEndian.PutUint16(data[44+(i*channels+ch)*2:], uint16(v))
		}
	}
	return data
}

// A clipped recording has runs of samples stuck at full scale. One peak at full
// scale, or two in a row, can be a loud hit that did not clip.
func TestDecodeWAVMarksRunsAtFullScale(t *testing.T) {
	frames := make([][]int16, 60)
	for i := range frames {
		frames[i] = []int16{1000}
	}
	set := func(value int16, at ...int) {
		for _, i := range at {
			frames[i][0] = value
		}
	}
	set(32767, 10, 11, 12, 13, 14) // five in a row: clipped
	set(32767, 20, 21)             // two in a row: not clipped
	set(32767, 30, 32, 34)         // never three in a row: not clipped
	set(-32768, 40, 41, 42)        // three at the negative rail: clipped
	set(32767, 50)                 // a lone peak: not clipped
	set(32766, 52, 53, 54)         // within a few codes of full scale: clipped
	audio, err := DecodeWAV(wav16(frames))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ from, to, want int }{
		{0, 60, 5 + 3 + 3}, {10, 15, 5}, {10, 12, 2}, {12, 20, 3}, {15, 40, 0}, {40, 43, 3},
		{20, 22, 0}, {30, 35, 0}, {50, 51, 0}, {52, 55, 3}, {-5, 11, 1}, {53, 5000, 2}, {20, 20, 0}, {30, 10, 0},
	} {
		if got := audio.ClippedFrames(test.from, test.to); got != test.want {
			t.Errorf("clipped frames in %d..%d: %d, want %d", test.from, test.to, got, test.want)
		}
	}
}

// A stereo file from an interface with one microphone often has one silent
// channel. Mixing down halves a clipped peak, so only the channel shows it.
func TestDecodeWAVFindsClippingInOneChannelOfAStereoFile(t *testing.T) {
	frames := make([][]int16, 40)
	for i := range frames {
		frames[i] = []int16{500, 0}
	}
	for i := 5; i < 9; i++ {
		frames[i][0] = 32767
	}
	// The channels take turns at the rail, so neither has three in a row.
	for i := 20; i < 30; i++ {
		frames[i][i%2] = 32767
	}
	audio, err := DecodeWAV(wav16(frames))
	if err != nil {
		t.Fatal(err)
	}
	if audio.PCM[6] > .51 {
		t.Fatalf("the mixed-down peak is %g; the test needs it near .5", audio.PCM[6])
	}
	if got, want := audio.ClippedFrames(0, 40), 4; got != want {
		t.Errorf("clipped frames: %d, want %d", got, want)
	}
}

func TestDecodeWAVMarksClippedFloatAndLeavesQuietAudioAlone(t *testing.T) {
	pcm := make([]float32, 30)
	for i := range pcm {
		pcm[i] = .5
	}
	pcm[3], pcm[4], pcm[5] = 1, 1.2, .99995
	pcm[10], pcm[11] = 1, 1
	audio, err := DecodeWAV(EncodeWAV(pcm, 48000))
	if err != nil {
		t.Fatal(err)
	}
	if got := audio.ClippedFrames(0, 30); got != 3 {
		t.Errorf("clipped frames: %d, want 3", got)
	}
	quiet, err := DecodeWAV(EncodeWAV(make([]float32, 100), 48000))
	if err != nil || quiet.clipped != nil || quiet.ClippedFrames(0, 100) != 0 {
		t.Errorf("silence marked as clipped: %v", err)
	}
	// Audio that did not come from DecodeWAV knows nothing of clipping.
	if got := (Audio{PCM: pcm, Rate: 48000}).ClippedFrames(0, 30); got != 0 {
		t.Errorf("hand-made audio reports %d clipped frames", got)
	}
}
