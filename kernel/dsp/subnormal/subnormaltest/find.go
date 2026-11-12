// Package subnormaltest finds subnormal numbers in a processor's state, for
// tests that check that decaying state never stalls in them.
package subnormaltest

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"testing"

	"m31labs.dev/cicada/kernel/dsp/subnormal"
)

// Find walks v (a pointer to a processor, usually) through structs, arrays,
// slices and pointers, including unexported fields, and returns the path of
// every float32 or float64 that holds a subnormal number, up to limit paths.
func Find(v any, limit int) []string {
	var found []string
	seen := map[uintptr]bool{}
	var walk func(value reflect.Value, path string)
	walk = func(value reflect.Value, path string) {
		if len(found) >= limit {
			return
		}
		switch value.Kind() {
		case reflect.Float32:
			bits := math.Float32bits(float32(value.Float()))
			if bits&0x7f800000 == 0 && bits&0x7fffffff != 0 {
				found = append(found, path)
			}
		case reflect.Float64:
			bits := math.Float64bits(value.Float())
			if bits&0x7ff0000000000000 == 0 && bits&0x7fffffffffffffff != 0 {
				found = append(found, path)
			}
		case reflect.Pointer:
			if value.IsNil() || seen[value.Pointer()] {
				return
			}
			seen[value.Pointer()] = true
			walk(value.Elem(), path)
		case reflect.Struct:
			for i := range value.NumField() {
				walk(value.Field(i), path+"."+value.Type().Field(i).Name)
			}
		case reflect.Array, reflect.Slice:
			for i := range value.Len() {
				walk(value.Index(i), fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(reflect.ValueOf(v), "")
	return found
}

// Hash is FNV-1a over the float32 bits of a stereo output, with one sum per
// second as well, so two architectures can compare a long render and find the
// first second where they differ.
type Hash struct {
	all, second uint64
	frames      int
	rate        int
	Seconds     []uint64
}

// NewHash starts a hash of output at rate frames per second.
func NewHash(rate int) *Hash {
	return &Hash{all: fnvOffset, second: fnvOffset, rate: rate}
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Add hashes one stereo frame.
func (h *Hash) Add(left, right float32) {
	for _, value := range [2]float32{left, right} {
		bits := math.Float32bits(value)
		for range 4 {
			h.all = (h.all ^ uint64(byte(bits))) * fnvPrime
			h.second = (h.second ^ uint64(byte(bits))) * fnvPrime
			bits >>= 8
		}
	}
	h.frames++
	if h.frames%h.rate == 0 {
		h.Seconds = append(h.Seconds, h.second)
		h.second = fnvOffset
	}
}

// Sum is the hash of every frame added.
func (h *Hash) Sum() uint64 { return h.all }

// Report formats the hash and the per-second hashes for a test log.
func (h *Hash) Report() string {
	text := fmt.Sprintf("sum %#016x, %d frames; per second:", h.all, h.frames)
	for _, second := range h.Seconds {
		text += fmt.Sprintf(" %016x", second)
	}
	return text
}

// CheckPin fails the test when h does not match pinned, a hash that amd64 and
// arm64 produced alike. With CICADA_DECAY_HASHES=1 it also logs the hashes,
// which is how a pin is made: run the test on both architectures, compare the
// logged sums, and pin a sum only when they agree. A pinned value of zero
// means not pinned; the test then only logs.
func CheckPin(t testing.TB, name string, h *Hash, pinned uint64) {
	t.Helper()
	if os.Getenv("CICADA_DECAY_HASHES") == "1" || pinned == 0 {
		t.Logf("DECAY-HASH %s %s", name, h.Report())
	}
	if pinned != 0 && h.Sum() != pinned {
		t.Fatalf("%s output changed: %#016x, pinned %#016x (rerun with CICADA_DECAY_HASHES=1 for the per-second hashes)", name, h.Sum(), pinned)
	}
}

// Case is a processor driven into a state where values glide to zero: a
// control moved to a target of zero, or input that stops.
type Case struct {
	Name    string
	Rate    int
	Seconds int
	// Next renders one frame (and applies any change due at that frame).
	Next func(frame int) (float32, float32)
	// State is what Find walks: the processor.
	State any
	// Swept reports whether the processor swept its values on the frame
	// just rendered; Run checks for subnormal numbers only then.
	Swept func() bool
	// Stalls lists the paths, as Find reports them, that hold subnormal
	// numbers at the end when nothing is flushed: the proof that the case
	// reaches the stall the flush exists for.
	Stalls []string
	// Pinned is the output hash. It must hold with the build tag
	// cicada_noflush too, unless PinnedWithoutFlush is set.
	Pinned uint64
	// PinnedWithoutFlush, when not zero, is the output hash with the build
	// tag cicada_noflush, for a case whose float32 output the flush changes
	// (for example a negative subnormal sum that converts to -0 instead of
	// +0); the case's comment must say what differs.
	PinnedWithoutFlush uint64
}

// Run renders c. With flushing, the state must hold no subnormal number after
// any sweep that Swept reports. With the build tag cicada_noflush, every path
// in Stalls must hold one at the end. In both builds the output must match
// the pin.
func Run(t *testing.T, c Case) {
	t.Helper()
	hash := NewHash(c.Rate)
	for frame := range c.Seconds * c.Rate {
		hash.Add(c.Next(frame))
		if subnormal.Enabled && c.Swept() {
			if found := Find(c.State, 5); len(found) > 0 {
				t.Fatalf("%s: after the sweep at %.2f s the state holds subnormal numbers: %v", c.Name, float64(frame)/float64(c.Rate), found)
			}
		}
	}
	pinned := c.Pinned
	if !subnormal.Enabled {
		if c.PinnedWithoutFlush != 0 {
			pinned = c.PinnedWithoutFlush
		}
		found := Find(c.State, 1<<20)
		for _, path := range c.Stalls {
			if !slices.Contains(found, path) {
				t.Errorf("%s: without flushing, %s holds no subnormal number at the end, so the case never reaches the stall (subnormal: %d values)", c.Name, path, len(found))
			}
		}
	}
	CheckPin(t, c.Name, hash, pinned)
}

// SkipWithoutFlush skips a test whose checks hold only when the processors
// flush: in a build with the tag cicada_noflush, where Flush and Flush64
// return their argument, its state stalls in subnormal numbers and its pinned
// output differs by design.
func SkipWithoutFlush(t testing.TB) {
	t.Helper()
	if !subnormal.Enabled {
		t.Skip("built with cicada_noflush: this test checks the flush itself")
	}
}

// Noise returns a deterministic noise source in [-1, 1).
func Noise(seed uint32) func() float32 {
	return func() float32 {
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return float32(int32(seed)) / (1 << 31)
	}
}
