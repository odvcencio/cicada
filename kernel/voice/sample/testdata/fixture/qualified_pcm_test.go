package fixture

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestPCMMatchesQualifiedSource(t *testing.T) {
	// Captured from ed2c4f35918e8cb212544c2bfd9686b663280328 before moving
	// preparation. Each digest includes both planar channels at all block sizes.
	want := [...]string{
		"31058bba93d97d679d0243b7da55ce9b94bf86fc1eda5eeebfefc75e18995963",
		"9b0b589bf1fd2a402d30a0740f4623b862b0a86ac396eb82919086054b6b945b",
		"2d0b9ea969c39851c69790a42c649875213f6ab3e52cd3bddcea2c817480d7ae",
		"3c9824edd55364499149f1415efe3ab099e9a7ce5a76a3ef2f0c8a7e74401503",
		"42c95b624b24a882a4db75d560fcd170065ea7d9850f3eb80d0f4dd2b22d8200",
		"c7dda33f6bb705a9f547a5d46771b7654e5c8ec65e75222be51ec584e5227dad",
	}
	var pcm [Frames * 2]float32
	for index := 0; index < Cases; index++ {
		for _, size := range []int{64, 128, 256} {
			if err := Render(index, size, pcm[:]); err != nil {
				t.Fatal(err)
			}
			hash := sha256.New()
			var data [4]byte
			for _, value := range pcm {
				binary.LittleEndian.PutUint32(data[:], math.Float32bits(value))
				_, _ = hash.Write(data[:])
			}
			if got := fmt.Sprintf("%x", hash.Sum(nil)); got != want[index] {
				t.Errorf("case %d block %d PCM bits changed: %s", index, size, got)
			}
		}
	}
}
