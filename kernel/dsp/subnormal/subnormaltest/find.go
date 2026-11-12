// Package subnormaltest finds subnormal numbers in a processor's state, for
// tests that check that decaying state never stalls in them.
package subnormaltest

import (
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
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

// From returns a hash whose Sum is FNV-1a over the per-second hashes from
// second on, and whose per-second hashes are those seconds'.
func (h *Hash) From(second int) *Hash {
	out := &Hash{all: fnvOffset, second: fnvOffset, rate: h.rate, frames: h.frames - second*h.rate}
	for _, s := range h.Seconds[min(second, len(h.Seconds)):] {
		out.Seconds = append(out.Seconds, s)
		for range 8 {
			out.all = (out.all ^ uint64(byte(s))) * fnvPrime
			s >>= 8
		}
	}
	return out
}

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
	// reaches the stall the flush exists for. A path also matches the
	// elements and fields below it (".input[1]" matches ".input[1][0].buffer[7]").
	Stalls []string
	// Unflushed lists paths that may hold subnormal numbers in both builds:
	// values the processor deliberately does not flush, because a flush
	// would change its output (each test says why). Matched like Stalls.
	Unflushed []string
	// Pinned is the output hash. It must hold with the build tag
	// cicada_noflush too, unless PinnedWithoutFlush is set.
	Pinned uint64
	// PinFromSecond, when not zero, pins the hash of the per-second hashes
	// from that second on (SumFrom) instead of the whole output: for a
	// processor whose first seconds differ between architectures for a
	// reason outside the case.
	PinFromSecond int
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
			var found []string
			for _, path := range Find(c.State, 1<<16) {
				if !matchAny(path, c.Unflushed) {
					found = append(found, path)
				}
			}
			if len(found) > 0 {
				t.Fatalf("%s: after the sweep at %.2f s the state holds subnormal numbers: %v", c.Name, float64(frame)/float64(c.Rate), found[:min(len(found), 5)])
			}
		}
	}
	pinned := c.Pinned
	if !subnormal.Enabled {
		if c.PinnedWithoutFlush != 0 {
			pinned = c.PinnedWithoutFlush
		}
		CheckStalls(t, c.Name, c.State, c.Stalls)
	}
	if c.PinFromSecond > 0 {
		if os.Getenv("CICADA_DECAY_HASHES") == "1" {
			t.Logf("DECAY-HASH %s (whole output, not pinned) %s", c.Name, hash.Report())
		}
		hash = hash.From(c.PinFromSecond)
	}
	CheckPin(t, c.Name, hash, pinned)
}

// CheckStalls fails the test unless every path in stalls (matched as in
// Case.Stalls) holds a subnormal number in state. A test calls it in a build
// with the tag cicada_noflush, to show that its case reaches the stall.
func CheckStalls(t testing.TB, name string, state any, stalls []string) {
	t.Helper()
	found := Find(state, 1<<20)
	for _, path := range stalls {
		hit := false
		for _, f := range found {
			if matchAny(f, []string{path}) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("%s: without flushing, %s holds no subnormal number at the end, so the case never reaches the stall (subnormal: %d values)", name, path, len(found))
		}
	}
}

// matchAny reports whether path is one of prefixes or lies below one.
func matchAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+"[") || strings.HasPrefix(path, p+".") {
			return true
		}
	}
	return false
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
