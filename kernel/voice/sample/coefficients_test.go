package sample

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestSRCCoefficientsMatchQualifiedSource(t *testing.T) {
	// Little-endian float32 digests captured from the unchanged package-init
	// generator at ed2c4f35918e8cb212544c2bfd9686b663280328, native amd64.
	want := [...]string{
		"3db50a0f99fa4cdf503f115c5b75a9614f6f9429ddedf53db92fee150f7c8081",
		"964fc9c8e36720aa6481f12620b18cb9e41ddbee30077717e2d704897b8998d8",
		"91d7a8d536b013a88567c37b045ad5702806f652007b9ef6fb76bb9dd19287f8",
		"8729b9801fe0828991c50b8f9a527732537619334568eeefef84c63a0f7e7299",
		"c217b1f799333fc8324ce69b80a48e0bae648df5883fd8b7a878982a369ce020",
		"bb949aa5de15de86f50e2d190a4d4ae9662b81d5af926a9ca849714d88f39e33",
		"ba59fe8622db5f99b00c67c3c2b2b924764ee7326664eab496a817fb5aab079d",
		"73a844f3f3046cd26621e52d6cefcabaeaa1930974dbcd639c62d7912253db17",
	}
	if _, err := New(48000, testRegion(2, false, false)); err != nil {
		t.Fatal(err)
	}
	for i, bank := range banks {
		hash := sha256.New()
		var data [4]byte
		for _, value := range bank.coeff {
			binary.LittleEndian.PutUint32(data[:], math.Float32bits(value))
			_, _ = hash.Write(data[:])
		}
		if got := fmt.Sprintf("%x", hash.Sum(nil)); got != want[i] {
			t.Errorf("bank %d coefficient bits changed: %s", i, got)
		}
	}
}
